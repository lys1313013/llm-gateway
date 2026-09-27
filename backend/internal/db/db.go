// Package db wraps a pgxpool with the project's table schema and CRUD helpers.
//
// Layout:
//   - db.go:         pool, Connect / Init / Close
//   - migrations.go: 迁移集（版本 → 语句），checksum
//   - migrate.go:    Migrate：advisory lock、逐版本事务、写版本行
//   - verify.go:     VerifySchema / ReadSchemaStatus：只读的版本校验
//   - providers.go:  provider CRUD
//   - routes.go:     model_route CRUD + active queries used by the proxy
//   - exposed.go:    exposed_model CRUD
//   - users.go:      user CRUD
//   - api_keys.go:   api_key CRUD
//   - logs.go:       api_log insert + log/stats queries
//
// 结构变更走 `gateway migrate` 子命令，**不在启动路径上**：Init 只开池 +
// 只读校验版本，不匹配就拒绝启动。这样本机 dev 后端连着生产库时，启动本身
// 不可能改动生产 schema。
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
)

// Pool is the global pgx connection pool.
var Pool *pgxpool.Pool

// 等 DB 就绪的节奏：单次 Ping 的上限，以及两次重试之间的间隔。总预算走
// config.DBConnectTimeoutS（环境变量 DB_CONNECT_TIMEOUT，默认 60 秒）。
const (
	dbPingTimeout          = 5 * time.Second
	dbConnectRetryInterval = 2 * time.Second
)

// Connect 打开连接池，并在 DB_CONNECT_TIMEOUT 预算内等 DB 就绪。
//
// 它**不校验版本、不跑迁移** —— 迁移路径（未迁移的库恰好校验不过）和测试
// 都需要一条"未校验"的连接。服务启动请用 Init。
func Connect(ctx context.Context) error {
	c := config.Get()

	cfg, err := pgxpool.ParseConfig(c.ConnInfo())
	if err != nil {
		// conninfo 写错了重试多少次都一样，不进重试循环。
		return fmt.Errorf("parse conninfo: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	// dial_ms 覆盖 NewWithConfig + 等 DB 就绪：生产上「容器起 → 第一行日志」
	// 的延迟几乎全在这里（DNS/网络/DB 未就绪时的握手等待），拆出来才知道慢在哪段。
	dialStart := time.Now()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}

	// 重试等 DB 就绪。postgres 在 docker/docker-compose.db.yml 里，与 backend
	// 不同 compose 文件、没有 depends_on，靠 restart: always 硬撑——机器重启或
	// DB 升级时 backend 常先起来。原来单次 Ping（5s 超时）失败即退出，会变成
	// 启动期崩溃循环，日志上看着像「启动慢」甚至「启动失败」。这里改成在
	// DB_CONNECT_TIMEOUT 秒的预算内反复试；不可恢复的配置错误上面已经返回了。
	budget := time.Duration(c.DBConnectTimeoutS) * time.Second
	deadline := time.Now().Add(budget)
	attempt := 0
	for {
		attempt++
		pingCtx, cancel := context.WithTimeout(ctx, dbPingTimeout)
		lastErr := pool.Ping(pingCtx)
		cancel()
		if lastErr == nil {
			break
		}
		// 外部要求退出（SIGTERM）就别再等了，让 main 走正常关闭。
		if ctx.Err() != nil {
			pool.Close()
			return fmt.Errorf("ping db: %w", ctx.Err())
		}
		// 下一次重试落在预算之外就放弃。每轮都会打 WARN，所以配置写错
		// （比如 DB_HOST 拼错）时错误信息立刻可见，只是退出被推迟了。
		if !time.Now().Add(dbConnectRetryInterval).Before(deadline) {
			pool.Close()
			return fmt.Errorf("ping db: gave up after %d attempts in %s: %w",
				attempt, budget, lastErr)
		}
		slog.Warn("db not ready, retrying",
			"attempt", attempt, "err", lastErr,
			"retry_in_ms", dbConnectRetryInterval.Milliseconds())
		select {
		case <-ctx.Done():
			pool.Close()
			return fmt.Errorf("ping db: %w", ctx.Err())
		case <-time.After(dbConnectRetryInterval):
		}
	}

	Pool = pool
	// 重试过就单独记一条：dial_ms 里含着等待，不说明白会以为是 DB 慢。
	if attempt > 1 {
		slog.Warn("db became ready after retries",
			"attempts", attempt, "waited_ms", time.Since(dialStart).Milliseconds())
	}
	slog.Info("database connection pool initialised",
		"max_conns", cfg.MaxConns, "min_conns", cfg.MinConns,
		"dial_ms", time.Since(dialStart).Milliseconds())

	return nil
}

// Init 开池并校验 schema 版本。服务启动路径用它 —— 校验放在这里而不是
// main 里，是为了让所有现有调用方自动被保护，不可能漏。
//
// 版本不匹配时返回 *SchemaMismatchError（从未迁移 / 库落后 / 文本被改动）；
// 「库比二进制新」只 WARN。详见 checkStatus。
func Init(ctx context.Context) error {
	if err := Connect(ctx); err != nil {
		return err
	}
	start := time.Now()
	if err := VerifySchema(ctx); err != nil {
		return err
	}
	slog.Info("schema version verified",
		"binary_version", latestVersion(),
		"verify_ms", time.Since(start).Milliseconds())
	return nil
}

// Close releases the pool.
func Close() {
	if Pool != nil {
		Pool.Close()
		Pool = nil
	}
}

// mustHavePool panics if the pool isn't ready — only for code paths called
// after Init returns.
func mustHavePool() *pgxpool.Pool {
	if Pool == nil {
		panic("db.Pool used before db.Init")
	}
	return Pool
}

// withTx runs fn inside a transaction with automatic rollback on error.
func withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := mustHavePool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
