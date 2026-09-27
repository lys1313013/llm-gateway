package db

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// statement 是一条迁移语句。warn 非空时，执行影响行数 > 0 会按这段文案打
// WARN —— 只用于一次性数据清理：正常情况下这些语句影响 0 行，一旦影响到行
// 就说明这个库确实带着旧数据，值得在日志里留痕。
type statement struct {
	sql  string
	warn string
}

// migration 是一个版本。stmts 按序在**同一个事务**里执行（PostgreSQL 的
// DDL 是事务性的），成功才写 schema_migrations 行。
type migration struct {
	version int
	name    string
	stmts   []statement
}

// migrations 是全量迁移集，version 必须从 1 开始连续递增（有测试钉住）。
//
// 追加新版本时的规则：
//   - 只追加，不改动已发布版本的 SQL 文本 —— 文本参与 checksum，改了就报错。
//   - 不要在里面用 CREATE INDEX CONCURRENTLY / VACUUM：它们不能跑在事务里，
//     而这里每个版本就是一个事务（有测试拦这个正则）。
var migrations = []migration{
	{version: 1, name: "baseline", stmts: baselineStmts},
}

// baselineStmts 是 version 1 的全部语句，顺序是硬约束，不得重排：
//
//	ddl → backfills → cleanup → constraints → indexes
//
// 理由是：team/users 必须先于引用它们的 api_logs/api_keys 建出来；cleanup
// 必须先于 constraints（先清掉 team_id 的 NULL 才能收紧 NOT NULL）；indexes
// 依赖 backfill 出来的列。
//
// v1 同时覆盖两条路径，这是刻意的：空库跑它是"从零建全表"，存量库跑它是
// "把旧结构补齐"。全部语句都幂等，所以不需要单独的 baseline 脚本。
var baselineStmts = concatStmts(
	ddlStmts,
	backfillStmts,
	cleanupStmts,
	constraintStmts,
	indexStmts,
)

func concatStmts(groups ...[]statement) []statement {
	var out []statement
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// ddlStmts 建表。被 REFERENCES 的表必须先建：team / users 排在最前，因为
// api_keys、api_logs 在建表语句里直接引用它们——空库首次启动时顺序错了会
// 直接 `relation "users" does not exist` 退出。
// 跨表外键若加不进 CREATE TABLE（见 backfillStmts 里的 team_id），就只能走 ALTER。
var ddlStmts = []statement{
	{sql: `CREATE TABLE IF NOT EXISTS team (
		id SERIAL PRIMARY KEY,
		name VARCHAR(100) UNIQUE NOT NULL,
		create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	)`},
	{sql: `CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		username VARCHAR(100) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		is_active BOOLEAN DEFAULT TRUE,
		role INTEGER NOT NULL DEFAULT 3,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	)`},
	{sql: `CREATE TABLE IF NOT EXISTS provider (
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
	)`},
	{sql: `CREATE TABLE IF NOT EXISTS api_logs (
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
	)`},
	{sql: `CREATE TABLE IF NOT EXISTS api_keys (
		id SERIAL PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		key_hash VARCHAR(255) NOT NULL,
		key_prefix VARCHAR(16) NOT NULL,
		key_value VARCHAR(255),
		name VARCHAR(100) NOT NULL DEFAULT 'default',
		is_active BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		last_used_at TIMESTAMP WITH TIME ZONE
	)`},
	{sql: `CREATE TABLE IF NOT EXISTS model_route (
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
	)`},
	{sql: `CREATE TABLE IF NOT EXISTS exposed_model (
		id SERIAL PRIMARY KEY,
		-- 唯一性由 (model_id, team_id) 复合索引负责，见 indexStmts
		model_id VARCHAR(255) NOT NULL,
		owned_by VARCHAR(100) DEFAULT 'organization',
		is_active BOOLEAN DEFAULT TRUE,
		last_openai_test_time TIMESTAMP WITH TIME ZONE,
		last_anthropic_test_time TIMESTAMP WITH TIME ZONE,
		last_openai_test_status VARCHAR(16),
		last_anthropic_test_status VARCHAR(16),
		create_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		update_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
	)`},
	{sql: `CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)`},
}

// backfillStmts 给老 schema 补列/补默认值。
var backfillStmts = []statement{
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS usage_data JSONB`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cache_creation_input_tokens INTEGER`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS cache_read_input_tokens INTEGER`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS request_headers JSONB`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS response_headers JSONB`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS provider_id INTEGER REFERENCES provider(id) ON DELETE SET NULL`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS provider_name VARCHAR(100)`},
	{sql: `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_value VARCHAR(255)`},
	{sql: `ALTER TABLE model_route ALTER COLUMN timeout SET DEFAULT -1`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_url VARCHAR(255)`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_format VARCHAR(32)`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS responses_base_url VARCHAR(255)`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_access_token TEXT`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_refresh_token TEXT`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS quota_account_id VARCHAR(255)`},
	{sql: `ALTER TABLE provider ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS session_id VARCHAR(128)`},
	{sql: `ALTER TABLE users ADD COLUMN IF NOT EXISTS role INTEGER NOT NULL DEFAULT 3`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS user_id INTEGER REFERENCES users(id) ON DELETE SET NULL`},
	{sql: `ALTER TABLE users ADD COLUMN IF NOT EXISTS team_id INTEGER REFERENCES team(id) ON DELETE SET NULL`},
	{sql: `ALTER TABLE model_route ADD COLUMN IF NOT EXISTS team_id INTEGER REFERENCES team(id) ON DELETE RESTRICT`},
	{sql: `ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS team_id INTEGER REFERENCES team(id) ON DELETE RESTRICT`},
	{sql: `ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS last_openai_test_status VARCHAR(16)`},
	{sql: `ALTER TABLE exposed_model ADD COLUMN IF NOT EXISTS last_anthropic_test_status VARCHAR(16)`},
	{sql: `ALTER TABLE api_logs ADD COLUMN IF NOT EXISTS last_message_preview VARCHAR(100)`},
}

// cleanupStmts 是一次性数据迁移：多租户改造（exposed_model / model_route
// 必须归属某个团队，不再有"team_id 为空 = 全局可见"的语义）+ 早期部署的
// role 补齐。三条都是自消解的——执行过一次之后永远匹配 0 行，所以它们
// 放在 v1 里对空库也安全。
//
// 这些语句此前在每次启动时执行（role 那条在 main.bootstrapRoot 里），
// 版本化之后只跑一次。可以安全移除那个每启动必跑的行为：role 列现在由
// backfillStmts 保证 NOT NULL DEFAULT 3，NULL 已不可能；CreateUser 显式
// 传 role。所以 v1 跑过之后没有人能把 role 置回 0/NULL。
var cleanupStmts = []statement{
	{
		sql:  `UPDATE model_route SET team_id = (SELECT MIN(id) FROM team) WHERE team_id IS NULL AND EXISTS (SELECT 1 FROM team)`,
		warn: "model_route 存量记录已归属到最早创建的团队",
	},
	{
		sql:  `DELETE FROM exposed_model WHERE team_id IS NULL`,
		warn: "已删除 team_id 为空的 exposed_model 记录（原「全局可见」语义）",
	},
	{
		sql:  `UPDATE users SET role = 1 WHERE role = 0 OR role IS NULL`,
		warn: "存量用户 role 已升级为 root（role=0/NULL 的历史数据）",
	},
}

// constraintStmts 收紧约束。刻意不加「有残留就跳过」的守卫：跳过的后果是
// 列仍可空而 Go 侧按 int 扫描，运行期每次查询都 500，比迁移时明确报错难查得多。
//
// 注意：cleanupStmts 必须先跑完，否则这里会在存量 NULL 上直接失败。
// 对空库来说这几条全部命中 no-op（CREATE TABLE 里已经带了 NOT NULL 和
// ON DELETE RESTRICT）。
var constraintStmts = []statement{
	{sql: `ALTER TABLE exposed_model ALTER COLUMN team_id SET NOT NULL`},
	{sql: `ALTER TABLE model_route ALTER COLUMN team_id SET NOT NULL`},
	// 外键改成 ON DELETE RESTRICT。按列名从 pg_constraint 反查约束名，
	// 不猜：猜错时 DROP CONSTRAINT IF EXISTS 是静默 no-op，会把
	// 「别的团队仍然加不了同名模型」这个 bug 原样留下。
	{sql: `DO $$ DECLARE cname text; BEGIN
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
	END $$`},
	{sql: `DO $$ DECLARE cname text; BEGIN
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
	END $$`},
	// 去掉 model_id 的全局唯一，改由 indexStmts 的 (model_id, team_id) 复合唯一索引承担。
	{sql: `DO $$ DECLARE cname text; BEGIN
		SELECT conname INTO cname FROM pg_constraint
		 WHERE conrelid = 'exposed_model'::regclass AND contype = 'u'
		   AND conkey = ARRAY[(SELECT attnum FROM pg_attribute
		                       WHERE attrelid = 'exposed_model'::regclass AND attname = 'model_id')];
		IF cname IS NOT NULL THEN
			EXECUTE format('ALTER TABLE exposed_model DROP CONSTRAINT %I', cname);
		END IF;
	END $$`},
}

// indexStmts 建依赖 backfill 列的索引，单独一组：老 schema 缺列时
// CREATE TABLE IF NOT EXISTS 那条路径不会失败。
var indexStmts = []statement{
	{sql: `CREATE INDEX IF NOT EXISTS idx_api_logs_session_id ON api_logs(session_id) WHERE session_id IS NOT NULL`},
	{sql: `CREATE INDEX IF NOT EXISTS idx_api_logs_user_id_created_at ON api_logs(user_id, created_at DESC)`},
	{sql: `CREATE INDEX IF NOT EXISTS idx_users_team_id ON users(team_id) WHERE team_id IS NOT NULL`},
	{sql: `CREATE INDEX IF NOT EXISTS idx_exposed_model_team_id ON exposed_model(team_id) WHERE team_id IS NOT NULL`},
	// 同名模型可以被不同团队各自登记一条
	{sql: `CREATE UNIQUE INDEX IF NOT EXISTS uq_exposed_model_model_team ON exposed_model(model_id, team_id)`},
	{sql: `CREATE INDEX IF NOT EXISTS idx_model_route_team_id ON model_route(team_id)`},
}

// Checksum 是某个版本全部 SQL 文本的 sha256（十六进制小写）。
//
// 只覆盖 sql、不含 warn 文案：改一句日志措辞不该触发重新盖章。
// 哈希前先做空白归一，否则重排缩进/换行就会误报漂移。
func (m migration) Checksum() string {
	var b strings.Builder
	for _, st := range m.stmts {
		b.WriteString(normalizeSQL(st.sql))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// normalizeSQL 把连续空白折叠成单个空格并去掉首尾空白。
//
// 只用于算 checksum，**绝不会拿去执行**——所以把 `--` 行注释和下一行折进
// 同一行这类副作用无所谓：执行的是 statement.sql 原文。
func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// LatestVersion 是二进制里最新的迁移版本；没有迁移时返回 0。
// 供 `gateway migrate -status` 展示「二进制期望什么」。
func LatestVersion() int { return latestVersion() }

// latestVersion 是二进制里最新的迁移版本；没有迁移时返回 0。
func latestVersion() int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].version
}
