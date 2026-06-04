// Command gateway is the LLM Gateway server (Go rewrite).
//
// It implements the same HTTP surface as the Python Flask backend so the
// React admin UI and any LLM client SDK can talk to either interchangeably
// (assuming the matching URL is configured).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lys1313013/llm-gateway/backend/internal/auth"
	"github.com/lys1313013/llm-gateway/backend/internal/config"
	"github.com/lys1313013/llm-gateway/backend/internal/db"
	"github.com/lys1313013/llm-gateway/backend/internal/handlers"
	"github.com/lys1313013/llm-gateway/backend/internal/middleware"
)

var version = "dev"

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Println("llm-gateway-backend", version)
		return
	}

	cfg := config.Load()

	// Logger
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// DB
	if err := db.Init(ctx); err != nil {
		slog.Error("db init", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Bootstrap: create default admin if no users exist
	if err := bootstrapAdmin(ctx); err != nil {
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

	go func() {
		slog.Info("listening", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen", "err", err)
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

func registerRoutes(r *gin.Engine) {
	// Health
	r.GET("/api/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// /v1/* — public API key auth
	r.GET("/v1/models", handlers.ListModels)
	r.POST("/v1/chat/completions", handlers.ChatCompletions)
	r.POST("/v1/messages", handlers.AnthropicMessages)

	// /api/auth/* — auth (login/register are whitelisted, the rest need JWT)
	authGrp := r.Group("/api/auth")
	{
		authGrp.POST("/login", handlers.Login)
		authGrp.POST("/register", handlers.Register)
		authGrp.GET("/me", handlers.Me)
		authGrp.PUT("/change_password", handlers.ChangePassword)
		authGrp.GET("/users", handlers.ListUsers)
		authGrp.DELETE("/users/:user_id", handlers.RemoveUser)
		authGrp.GET("/api_keys", handlers.ListAPIKeys)
		authGrp.POST("/api_keys", handlers.CreateAPIKey)
		authGrp.DELETE("/api_keys/:key_id", handlers.DeleteAPIKey)
		authGrp.PUT("/api_keys/:key_id/toggle", handlers.ToggleAPIKey)
		authGrp.PUT("/api_keys/:key_id", handlers.UpdateAPIKey)
	}

	// /api/* — admin (JWT auth)
	r.GET("/api/provider", handlers.ListProviders)
	r.GET("/api/provider/:id", handlers.GetProvider)
	r.POST("/api/provider", handlers.CreateProvider)
	r.PUT("/api/provider/:id", handlers.UpdateProvider)
	r.DELETE("/api/provider/:id", handlers.DeleteProvider)

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

	// Logs / stats
	r.GET("/api/logs", handlers.ListLogs)
	r.GET("/api/logs/:id", handlers.GetLogDetail)
	r.GET("/api/logs/today_stats", handlers.TodayStats)
	r.GET("/api/stats/daily_tokens", handlers.DailyTokenStats)

	// Admin test endpoints
	r.POST("/api/test/chat", handlers.TestChat)
	r.POST("/api/test/messages", handlers.TestMessages)
}

// bootstrapAdmin creates an `admin` user with the default password if the
// users table is empty. Mirrors the Python backend's behaviour in app.py.
func bootstrapAdmin(ctx context.Context) error {
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
	if _, err := db.CreateUser(ctx, "admin", hash); err != nil {
		return err
	}
	slog.Info("Default admin user created (username: admin, password: llm_gateway)")
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
