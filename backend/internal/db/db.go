// Package db wraps a pgxpool with the project's table schema and CRUD helpers.
//
// Layout:
//   - db.go: pool, schema init
//   - providers.go: provider CRUD
//   - routes.go:    model_route CRUD + active queries used by the proxy
//   - exposed.go:   exposed_model CRUD
//   - users.go:     user CRUD
//   - api_keys.go:  api_key CRUD
//   - logs.go:      api_log insert + log/stats queries
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

// Init opens the pool and creates tables. Safe to call multiple times.
func Init(ctx context.Context) error {
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

	return initSchema(ctx)
}

// Close releases the pool.
func Close() {
	if Pool != nil {
		Pool.Close()
		Pool = nil
	}
}

func initSchema(ctx context.Context) error {
	start := time.Now()

	// 顺序有约束：被 REFERENCES 的表必须先建。team / users 排在最前，
	// 因为 api_keys、api_logs 都在建表语句里直接引用它们——空库首次启动
	// 时顺序错了会直接 `relation "users" does not exist` 退出。
	// 跨表外键若加不进 CREATE TABLE（见 backfills 里的 team_id），就只能走 ALTER。
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS team (
			id SERIAL PRIMARY KEY,
			name VARCHAR(100) UNIQUE NOT NULL,
			create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(100) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			is_active BOOLEAN DEFAULT TRUE,
			role INTEGER NOT NULL DEFAULT 3,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS provider (
			id SERIAL PRIMARY KEY,
			name VARCHAR(100) UNIQUE NOT NULL,
			openai_base_url VARCHAR(255),
			anthropic_base_url VARCHAR(255),
			responses_base_url VARCHAR(255),
			api_key VARCHAR(255),
			remark TEXT,
			quota_url VARCHAR(255),
			quota_format VARCHAR(32),
			quota_access_token TEXT,
			quota_refresh_token TEXT,
			quota_account_id VARCHAR(255),
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS api_logs (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			model VARCHAR(100),
			provider_id INTEGER REFERENCES provider(id) ON DELETE SET NULL,
			provider_name VARCHAR(100),
			is_stream BOOLEAN DEFAULT FALSE,
			status_code INTEGER,
			processing_time_ms INTEGER,
			prompt_tokens INTEGER,
			completion_tokens INTEGER,
			total_tokens INTEGER,
			target_url VARCHAR(255),
			request_data JSONB,
			response_data JSONB,
			request_headers JSONB,
			response_headers JSONB,
			error_message TEXT,
			protocol VARCHAR(50),
			usage_data JSONB,
			cache_creation_input_tokens INTEGER,
			cache_read_input_tokens INTEGER,
			session_id VARCHAR(128),
			user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
			last_message_preview VARCHAR(100)
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id SERIAL PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			key_hash VARCHAR(255) NOT NULL,
			key_prefix VARCHAR(16) NOT NULL,
			key_value VARCHAR(255),
			name VARCHAR(100) NOT NULL DEFAULT 'default',
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			last_used_at TIMESTAMP WITH TIME ZONE
		)`,
		`CREATE TABLE IF NOT EXISTS model_route (
			id SERIAL PRIMARY KEY,
			model_pattern VARCHAR(255) NOT NULL,
			route_type VARCHAR(20) NOT NULL,
			provider_id INTEGER REFERENCES provider(id) ON DELETE SET NULL,
			target_model VARCHAR(100),
			timeout INTEGER DEFAULT -1,
			log_requests BOOLEAN DEFAULT TRUE,
			log_responses BOOLEAN DEFAULT TRUE,
			priority INTEGER DEFAULT 0,
			is_active BOOLEAN DEFAULT TRUE,
			create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS exposed_model (
			id SERIAL PRIMARY KEY,
			-- 唯一性由 (model_id, team_id) 复合索引负责，见 indexes 段
			model_id VARCHAR(255) NOT NULL,
			owned_by VARCHAR(100) DEFAULT 'organization',
			is_active BOOLEAN DEFAULT TRUE,
			last_openai_test_time TIMESTAMP WITH TIME ZONE,
			last_anthropic_test_time TIMESTAMP WITH TIME ZONE,
			last_openai_test_status VARCHAR(16),
			last_anthropic_test_status VARCHAR(16),
			create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)`,
	}

	for _, sql := range ddl {
		if _, err := Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("ddl: %w (sql=%s)", err, sql)
		}
	}

	// Backfill defaults that may be missing on older schemas
	backfills := []string{
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS usage_data JSONB`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cache_creation_input_tokens INTEGER`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cache_read_input_tokens INTEGER`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS request_headers JSONB`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS response_headers JSONB`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS provider_id INTEGER REFERENCES provider(id) ON DELETE SET NULL`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS provider_name VARCHAR(100)`,
		`ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_value VARCHAR(255)`,
		`ALTER TABLE model_route ALTER COLUMN timeout SET DEFAULT -1`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_url VARCHAR(255)`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_format VARCHAR(32)`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS responses_base_url VARCHAR(255)`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_access_token TEXT`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_refresh_token TEXT`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_account_id VARCHAR(255)`,
		`ALTER TABLE provider ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS session_id VARCHAR(128)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS role INTEGER NOT NULL DEFAULT 3`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS user_id INTEGER REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS team_id INTEGER REFERENCES team(id) ON DELETE SET NULL`,
		`ALTER TABLE model_route ADD COLUMN IF NOT EXISTS team_id INTEGER REFERENCES team(id) ON DELETE RESTRICT`,
		`ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS team_id INTEGER REFERENCES team(id) ON DELETE RESTRICT`,
		`ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS last_openai_test_status VARCHAR(16)`,
		`ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS last_anthropic_test_status VARCHAR(16)`,
		`ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS last_message_preview VARCHAR(100)`,
	}
	for _, sql := range backfills {
		if _, err := Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("backfill: %w (sql=%s)", err, sql)
		}
	}

	// 多租户迁移：exposed_model / model_route 必须归属某个团队，不再有
	// "team_id 为空 = 全局可见"的语义。下面的顺序不能调换——必须先清掉
	// 存量 NULL 才能收紧 NOT NULL，否则启动直接失败。

	// 存量清理。两条都是自消解的：执行过一次之后永远匹配 0 行。
	for _, m := range []struct{ sql, msg string }{
		{`UPDATE model_route SET team_id = (SELECT MIN(id) FROM team) WHERE team_id IS NULL AND EXISTS (SELECT 1 FROM team)`,
			"model_route 存量记录已归属到最早创建的团队"},
		{`DELETE FROM exposed_model WHERE team_id IS NULL`,
			"已删除 team_id 为空的 exposed_model 记录（原「全局可见」语义）"},
	} {
		tag, err := Pool.Exec(ctx, m.sql)
		if err != nil {
			return fmt.Errorf("migration: %w (sql=%s)", err, m.sql)
		}
		if n := tag.RowsAffected(); n > 0 {
			slog.Warn("migration: "+m.msg, "rows", n)
		}
	}

	// 收紧非空。这里刻意不加「有残留就跳过」的守卫：跳过的后果是列仍可空
	// 而 Go 侧按 int 扫描，运行期每次查询都 500，比启动时明确报错难查得多。
	constraints := []string{
		`ALTER TABLE exposed_model ALTER COLUMN team_id SET NOT NULL`,
		`ALTER TABLE model_route ALTER COLUMN team_id SET NOT NULL`,
		// 外键改成 ON DELETE RESTRICT。按列名从 pg_constraint 反查约束名，
		// 不猜：猜错时 DROP CONSTRAINT IF EXISTS 是静默 no-op，会把
		// 「别的团队仍然加不了同名模型」这个 bug 原样留下。
		`DO $$ DECLARE cname text; BEGIN
			SELECT conname INTO cname FROM pg_constraint
			 WHERE conrelid = 'exposed_model'::regclass AND contype = 'f'
			   AND conkey = ARRAY[(SELECT attnum FROM pg_attribute
			                       WHERE attrelid = 'exposed_model'::regclass AND attname = 'team_id')];
			IF cname IS NULL THEN
				ALTER TABLE exposed_model ADD CONSTRAINT exposed_model_team_id_fkey
					FOREIGN KEY (team_id) REFERENCES team(id) ON DELETE RESTRICT;
			ELSIF (SELECT confdeltype FROM pg_constraint
			        WHERE conname = cname AND conrelid = 'exposed_model'::regclass) <> 'r' THEN
				EXECUTE format('ALTER TABLE exposed_model DROP CONSTRAINT %I', cname);
				ALTER TABLE exposed_model ADD CONSTRAINT exposed_model_team_id_fkey
					FOREIGN KEY (team_id) REFERENCES team(id) ON DELETE RESTRICT;
			END IF;
		END $$`,
		`DO $$ DECLARE cname text; BEGIN
			SELECT conname INTO cname FROM pg_constraint
			 WHERE conrelid = 'model_route'::regclass AND contype = 'f'
			   AND conkey = ARRAY[(SELECT attnum FROM pg_attribute
			                       WHERE attrelid = 'model_route'::regclass AND attname = 'team_id')];
			IF cname IS NULL THEN
				ALTER TABLE model_route ADD CONSTRAINT model_route_team_id_fkey
					FOREIGN KEY (team_id) REFERENCES team(id) ON DELETE RESTRICT;
			ELSIF (SELECT confdeltype FROM pg_constraint
			        WHERE conname = cname AND conrelid = 'model_route'::regclass) <> 'r' THEN
				EXECUTE format('ALTER TABLE model_route DROP CONSTRAINT %I', cname);
				ALTER TABLE model_route ADD CONSTRAINT model_route_team_id_fkey
					FOREIGN KEY (team_id) REFERENCES team(id) ON DELETE RESTRICT;
			END IF;
		END $$`,
		// 去掉 model_id 的全局唯一，改由下面的 (model_id, team_id) 复合唯一索引承担。
		`DO $$ DECLARE cname text; BEGIN
			SELECT conname INTO cname FROM pg_constraint
			 WHERE conrelid = 'exposed_model'::regclass AND contype = 'u'
			   AND conkey = ARRAY[(SELECT attnum FROM pg_attribute
			                       WHERE attrelid = 'exposed_model'::regclass AND attname = 'model_id')];
			IF cname IS NOT NULL THEN
				EXECUTE format('ALTER TABLE exposed_model DROP CONSTRAINT %I', cname);
			END IF;
		END $$`,
	}
	for _, sql := range constraints {
		if _, err := Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("migration: %w (sql=%s)", err, sql)
		}
	}

	// Indexes that depend on backfilled columns. Kept separate so the
	// CREATE TABLE IF NOT EXISTS path doesn't fail when a legacy schema
	// is missing the column.
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_api_logs_session_id ON api_logs(session_id) WHERE session_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_api_logs_user_id_created_at ON api_logs(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_users_team_id ON users(team_id) WHERE team_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_exposed_model_team_id ON exposed_model(team_id) WHERE team_id IS NOT NULL`,
		// 同名模型可以被不同团队各自登记一条
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_exposed_model_model_team ON exposed_model(model_id, team_id)`,
		`CREATE INDEX IF NOT EXISTS idx_model_route_team_id ON model_route(team_id)`,
	}
	for _, sql := range indexes {
		if _, err := Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("index: %w (sql=%s)", err, sql)
		}
	}

	slog.Info("database schema initialised", "migrate_ms", time.Since(start).Milliseconds())
	return nil
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
