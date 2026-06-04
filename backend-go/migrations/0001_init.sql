-- 0001_init.sql
-- LLM Gateway PostgreSQL schema (compatible with the Python backend)
-- The Go backend runs the same DDL on startup; this file documents the
-- canonical schema for reference and migration.

CREATE TABLE IF NOT EXISTS api_logs (
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
    error_message TEXT,
    protocol VARCHAR(50),
    usage_data JSONB,
    cache_creation_input_tokens INTEGER,
    cache_read_input_tokens INTEGER
);

CREATE TABLE IF NOT EXISTS provider (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) UNIQUE NOT NULL,
    openai_base_url VARCHAR(255),
    anthropic_base_url VARCHAR(255),
    api_key VARCHAR(255),
    remark TEXT,
    create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS model_route (
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
);

CREATE TABLE IF NOT EXISTS exposed_model (
    id SERIAL PRIMARY KEY,
    model_id VARCHAR(255) UNIQUE NOT NULL,
    owned_by VARCHAR(100) DEFAULT 'organization',
    is_active BOOLEAN DEFAULT TRUE,
    last_openai_test_time TIMESTAMP WITH TIME ZONE,
    last_anthropic_test_time TIMESTAMP WITH TIME ZONE,
    create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_hash VARCHAR(255) NOT NULL,
    key_prefix VARCHAR(16) NOT NULL,
    key_value VARCHAR(255),
    name VARCHAR(100) NOT NULL DEFAULT 'default',
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
