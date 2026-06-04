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

	"github.com/lys1313013/llm-gateway/backend-go/internal/config"
)

// Pool is the global pgx connection pool.
var Pool *pgxpool.Pool

// Init opens the pool and creates tables. Safe to call multiple times.
func Init(ctx context.Context) error {
	c := config.Get()

	cfg, err := pgxpool.ParseConfig(c.ConnInfo())
	if err != nil {
		return fmt.Errorf("parse conninfo: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return fmt.Errorf("ping db: %w", err)
	}

	Pool = pool
	slog.Info("database connection pool initialised",
		"max_conns", cfg.MaxConns, "min_conns", cfg.MinConns)

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
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS api_logs (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			model VARCHAR(100),
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
			cache_read_input_tokens INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS provider (
			id SERIAL PRIMARY KEY,
			name VARCHAR(100) UNIQUE NOT NULL,
			openai_base_url VARCHAR(255),
			anthropic_base_url VARCHAR(255),
			api_key VARCHAR(255),
			remark TEXT,
			create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
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
			model_id VARCHAR(255) UNIQUE NOT NULL,
			owned_by VARCHAR(100) DEFAULT 'organization',
			is_active BOOLEAN DEFAULT TRUE,
			last_openai_test_time TIMESTAMP WITH TIME ZONE,
			last_anthropic_test_time TIMESTAMP WITH TIME ZONE,
			create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(100) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
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
		`ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_value VARCHAR(255)`,
		`ALTER TABLE model_route ALTER COLUMN timeout SET DEFAULT -1`,
	}
	for _, sql := range backfills {
		if _, err := Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("backfill: %w (sql=%s)", err, sql)
		}
	}

	slog.Info("database schema initialised")
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
