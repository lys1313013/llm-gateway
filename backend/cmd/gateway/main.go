// Command gateway is the LLM Gateway server (Go rewrite).
//
// It implements the same HTTP surface as the Python Flask backend so the
// React admin UI and any LLM client SDK can talk to either interchangeably
// (assuming the matching URL is configured).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/auth"
	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/handlers"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
	"github.com/lys1313013/llm-gateway/backend/internal/quota"
)

var version = "dev"

func main() {
	// 起点早于 logger（logger 还没装配，打不出来）。startup_ms 报的是这段
	// 到实际可 accept 为止的墙钟时间，容器起 → 第一行日志那段黑盒靠它暴露。
	bootStart := time.Now()

	// 子命令分发：只在 os.Args[1] 不以 '-' 开头时发生。这个判断位置是关键 ——
	// -version 必须在 config.Load() 和任何 DB 动作之前返回，compose 的
	// healthcheck 就是 `["CMD", "/app/gateway", "-version"]`。
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(runSubcommand(os.Args[1], os.Args[2:]))
	}

	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Println("llm-gateway-backend", version)
		return
	}

	cfg := config.Load()
	setupLogger(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// DB：开池 + 只读校验 schema 版本。迁移不在这里跑，走 `gateway migrate`。
	if err := db.Init(ctx); err != nil {
		var mismatch *db.SchemaMismatchError
		if errors.As(err, &mismatch) {
			slog.Error("schema version mismatch", "err", mismatch.Error(), "hint", mismatch.Hint)
		} else {
			slog.Error("db init", "err", err)
		}
		os.Exit(1)
	}
	defer db.Close()

	// Quota fetcher + background refresher
	quotaFetcher := quota.NewFetcher()
	quota.InitGlobal(quotaFetcher)
	go quotaFetcher.RunRefresher(ctx, 5*time.Minute)

	// Bootstrap: create default admin if no users exist
	if err := bootstrapRoot(ctx); err != nil {
		slog.Error("bootstrap admin", "err", err)
	}

	// Router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())
	r.Use(requestLogger())
	r.Use(middleware.RequireAuth())

	registerRoutes(r)

	// HTTP server
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.HTTPPort),
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
	}

	// 先同步 bind 再交给 goroutine，两个原因：
	// 一是 startup_ms 要打在实际能 accept 之后，写在 goroutine 里会报成
	// 「刚要 listen」而不是「已经 listen 上」；二是端口被占时能在 main 里
	// 直接非 0 退出——原来的写法是 goroutine 里 cancel ctx，主流程走正常
	// 关闭路径 exit 0，面板显示「正常退出」，看不出是根本没起来。
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		slog.Error("listen", "addr", srv.Addr, "err", err)
		os.Exit(1)
	}
	slog.Info("listening",
		"port", cfg.HTTPPort,
		"startup_ms", time.Since(bootStart).Milliseconds())

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", "err", err)
			cancel()
		}
	}()

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		slog.Info("shutdown signal received")
	case <-ctx.Done():
	}

	shutdownCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
	defer sc()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
	slog.Info("server stopped")
}

func setupLogger(cfg *config.Config) {
	lvl := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

// runSubcommand 执行非服务类子命令并返回进程退出码。
//
// 退出码约定：0 成功/无变化，1 执行失败，2 未知子命令或参数错误
// （flag.ExitOnError 自己会以 2 退出，与这里一致）。
func runSubcommand(cmd string, args []string) int {
	switch cmd {
	case "migrate":
		fs := flag.NewFlagSet("migrate", flag.ExitOnError)
		statusOnly := fs.Bool("status", false, "只读查看版本状态，不执行迁移")
		_ = fs.Parse(args)

		setupLogger(config.Load())
		ctx := context.Background()

		if *statusOnly {
			if err := printSchemaStatus(ctx); err != nil {
				slog.Error("migrate -status", "err", err)
				return 1
			}
			return 0
		}

		if err := db.Migrate(ctx, db.MigrateOptions{AppVersion: version}); err != nil {
			var mismatch *db.SchemaMismatchError
			if errors.As(err, &mismatch) {
				slog.Error("migration refused", "err", mismatch.Error(), "hint", mismatch.Hint)
			} else {
				slog.Error("migrate", "err", err)
			}
			return 1
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr,
			"未知子命令 %q\n\n用法:\n"+
				"  gateway [-version]          启动服务\n"+
				"  gateway migrate [-status]   执行迁移 / 只读查看版本状态\n", cmd)
		return 2
	}
}

// printSchemaStatus 打印「库里有什么 / 二进制期望什么」，对应 `migrate -status`。
// 只读：不建表、不加锁、不迁移。
func printSchemaStatus(ctx context.Context) error {
	if err := db.Connect(ctx); err != nil {
		return err
	}
	defer db.Close()

	st, err := db.ReadSchemaStatus(ctx)
	if err != nil {
		return err
	}

	if !st.TableExists {
		slog.Info("schema 从未迁移（schema_migrations 表不存在）",
			"binary_version", db.LatestVersion())
		return nil
	}
	for _, r := range st.Applied {
		// execution_ms / app_version 在库里可空，直接交给 slog 会打成指针地址。
		slog.Info("applied",
			"version", r.Version, "name", r.Name,
			"applied_at", r.AppliedAt.Format(time.RFC3339),
			"execution_ms", derefInt(r.ExecutionMS), "app_version", derefStr(r.AppVersion))
	}
	slog.Info("schema status",
		"db_version", st.DBVersion,
		"binary_version", db.LatestVersion(),
		"pending", fmt.Sprint(st.Pending),
		"checksum_drift", fmt.Sprint(st.ChecksumDrift),
		"db_ahead_of_binary", st.Ahead)
	return nil
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func registerRoutes(r *gin.Engine) {
	// Health
	r.GET("/api/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// /v1/* — public API key auth
	r.GET("/v1/models", handlers.ListModels)
	r.POST("/v1/chat/completions", handlers.ChatCompletions)
	r.POST("/v1/messages", handlers.AnthropicMessages)
	r.POST("/v1/responses", handlers.Responses)

	// /api/auth/* — auth (login/register are whitelisted, the rest need JWT)
	authGrp := r.Group("/api/auth")
	{
		authGrp.POST("/login", handlers.Login)
		authGrp.POST("/register", handlers.Register)
		authGrp.GET("/me", handlers.Me)
		authGrp.PUT("/change_password", handlers.ChangePassword)
		authGrp.GET("/users", handlers.ListUsers)
		authGrp.DELETE("/users/:user_id", handlers.RemoveUser)
		authGrp.PUT("/users/:user_id/role", handlers.UpdateUserRole)
		authGrp.PUT("/users/:user_id/team", handlers.UpdateUserTeam)
		authGrp.GET("/api_keys", handlers.ListAPIKeys)
		authGrp.POST("/api_keys", handlers.CreateAPIKey)
		authGrp.DELETE("/api_keys/:key_id", handlers.DeleteAPIKey)
		authGrp.PUT("/api_keys/:key_id/toggle", handlers.ToggleAPIKey)
		authGrp.PUT("/api_keys/:key_id", handlers.UpdateAPIKey)
	}

	// /api/* — admin (JWT auth)
	r.GET("/api/provider", handlers.ListProviders)
	r.GET("/api/provider/presets", handlers.ListProviderPresets)
	r.GET("/api/provider/:id", handlers.GetProvider)
	r.POST("/api/provider", handlers.CreateProvider)
	r.PUT("/api/provider/:id", handlers.UpdateProvider)
	r.DELETE("/api/provider/:id", handlers.DeleteProvider)

	// Quota
	r.GET("/api/provider/quota", handlers.ListProviderQuotas)
	r.GET("/api/provider/:id/quota", handlers.GetProviderQuota)
	r.POST("/api/provider/:id/quota/refresh", handlers.RefreshProviderQuota)

	r.GET("/api/route", handlers.ListRoutes)
	r.GET("/api/route/:id", handlers.GetRoute)
	r.POST("/api/route", handlers.CreateRoute)
	r.PUT("/api/route/:id", handlers.UpdateRoute)
	r.DELETE("/api/route/:id", handlers.DeleteRoute)

	r.GET("/api/exposed_model", handlers.ListExposedModels)
	r.GET("/api/exposed_model/:id", handlers.GetExposedModel)
	r.POST("/api/exposed_model", handlers.CreateExposedModel)
	r.PUT("/api/exposed_model/:id", handlers.UpdateExposedModel)
	r.DELETE("/api/exposed_model/:id", handlers.DeleteExposedModel)
	r.PUT("/api/exposed_model/:id/test_time", handlers.UpdateExposedModelTestTime)

	// Team
	r.GET("/api/team", handlers.ListTeams)
	r.GET("/api/team/:id", handlers.GetTeam)
	r.POST("/api/team", handlers.CreateTeam)
	r.PUT("/api/team/:id", handlers.UpdateTeam)
	r.DELETE("/api/team/:id", handlers.DeleteTeam)

	// Logs / stats
	r.GET("/api/logs", handlers.ListLogs)
	r.GET("/api/logs/:id", handlers.GetLogDetail)
	r.DELETE("/api/logs/:id", handlers.DeleteLog)
	r.GET("/api/logs/today_stats", handlers.TodayStats)
	r.GET("/api/logs/status_codes", handlers.ListStatusCodes)
	r.GET("/api/stats/daily_tokens", handlers.DailyTokenStats)

	// Sessions — 按配置的 session id 请求头对 api_logs 进行聚合
	r.GET("/api/sessions", handlers.ListSessions)
	r.GET("/api/sessions/:id", handlers.GetSession)
	r.DELETE("/api/sessions/:id", handlers.DeleteSession)

	// Admin test endpoints
	r.POST("/api/test/chat", handlers.TestChat)
	r.POST("/api/test/messages", handlers.TestMessages)

	// Provider self-test — verify connectivity without persisting the
	// provider. Auto-detects configured protocol(s) and runs a full
	// list-and-chat cycle. Used by the "新增产商" form.
	r.POST("/api/provider/test/connect", handlers.ProviderConnect)
}

// bootstrapRoot creates a root user if no users exist. Mirrors the Python
// backend's behaviour in app.py.
//
// 「role=0/NULL 的存量用户升级为 root」原本也在这里、每次启动都跑，现已并进
// 迁移 v1 的一次性清理（见 db.cleanupStmts）。role 列由 backfill 保证
// NOT NULL DEFAULT 3、CreateUser 显式传 role，所以 v1 之后不会再出现 0/NULL。
func bootstrapRoot(ctx context.Context) error {
	n, err := db.GetUserCount(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	hash, err := auth.HashPassword("llm_gateway")
	if err != nil {
		return err
	}
	if _, err := db.CreateUser(ctx, "root", hash, 1); err != nil {
		return err
	}
	slog.Info("Default root user created (username: root, password: llm_gateway)")
	return nil
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		// Skip /api/healthz chatter
		if c.Request.URL.Path == "/api/healthz" {
			return
		}
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"size", c.Writer.Size(),
			"latency_ms", fmt.Sprintf("%.1f", float64(time.Since(start).Microseconds())/1000),
			"ip", c.ClientIP(),
		)
	}
}
