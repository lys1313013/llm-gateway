package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
)

// scratchDBName 是集成测试专用库，**硬编码、刻意忽略 TEST_DB_NAME**。
//
// 原因：handlers_test 里 TEST_DB_NAME 的默认值是 llm_gateway，也就是生产库名。
// 一旦 TEST_DB_HOST / TEST_DB_PASSWORD 指向生产，照搬那套默认值就会把迁移
// 直接打到生产库上。这几个用例会 DROP/CREATE DATABASE，所以库名必须是本文件
// 自己钉死的。
const scratchDBName = "llm_gateway_migrate_test"

// 同进程内所有用例共用 db.Pool 与 config 单例（都是全局），
// 因此本文件不能用 t.Parallel()。
func setupScratch(t *testing.T) {
	t.Helper()

	host := testRequireEnv(t, "TEST_DB_HOST")
	pass := testRequireEnv(t, "TEST_DB_PASSWORD")
	port := testEnvOr("TEST_DB_PORT", "5432")
	user := testEnvOr("TEST_DB_USER", "postgres")

	ctx := context.Background()

	// 建/删库得连另一条连接（不能删自己正连着的库），所以先连维护库 postgres。
	admin, err := pgx.Connect(ctx, fmt.Sprintf(
		"host=%s port=%s dbname=postgres user=%s password=%s", host, port, user, pass))
	if err != nil {
		t.Skipf("skipping: cannot connect to test db: %v", err)
	}

	recreate := func() error {
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+scratchDBName+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("drop scratch db: %w", err)
		}
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+scratchDBName); err != nil {
			return fmt.Errorf("create scratch db: %w", err)
		}
		return nil
	}
	if err := recreate(); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("%v", err)
	}

	// 环境变量先设好再 Load：godotenv 不会覆盖已存在的变量，所以 backend/.env
	// （本机指向生产库的那份）绝不会漏进来。
	os.Setenv("DB_HOST", host)
	os.Setenv("DB_PORT", port)
	os.Setenv("DB_USER", user)
	os.Setenv("DB_PASSWORD", pass)
	os.Setenv("DB_NAME", scratchDBName)
	// 等锁/等连接都只在同一个本机库上，预算给短一点，失败得快一些。
	os.Setenv("DB_CONNECT_TIMEOUT", "5")

	config.Load()

	if err := Connect(ctx); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("connect scratch db: %v", err)
	}

	t.Cleanup(func() {
		Close()
		if err := recreate(); err != nil {
			// 清理时先删后建，留一个干净的空库，避免残留半迁移状态干扰下次。
			t.Logf("cleanup: %v", err)
		}
		_ = admin.Close(context.Background())
	})
}

func testEnvOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func testRequireEnv(t *testing.T, key string) string {
	t.Helper()
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	t.Skipf("skipping: required env var %s is not set", key)
	return ""
}

func TestMigrateOnEmptyDatabase(t *testing.T) {
	setupScratch(t)
	ctx := context.Background()

	if err := Migrate(ctx, MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st, err := ReadSchemaStatus(ctx)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !st.TableExists {
		t.Fatal("schema_migrations 应该由迁移器建出来")
	}
	if st.DBVersion != LatestVersion() {
		t.Fatalf("db version = %d, want %d", st.DBVersion, LatestVersion())
	}
	if len(st.Pending) != 0 || len(st.ChecksumDrift) != 0 || st.Ahead {
		t.Fatalf("迁移完不该有 pending/drift/ahead: %+v", st)
	}
	if len(st.Applied) != 1 {
		t.Fatalf("版本行数 = %d, want 1", len(st.Applied))
	}
	if r := st.Applied[0]; r.Version != 1 || r.Name != "baseline" {
		t.Fatalf("版本行内容不对: %+v", r)
	}
	if r := st.Applied[0]; r.AppVersion == nil || *r.AppVersion != "test" {
		t.Fatalf("app_version 没落库: %v", r.AppVersion)
	}
	if r := st.Applied[0]; r.ExecutionMS == nil {
		t.Fatal("execution_ms 没落库")
	}

	for _, tbl := range []string{"team", "users", "provider", "api_logs", "api_keys", "model_route", "exposed_model"} {
		var reg *string
		if err := Pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, tbl).Scan(&reg); err != nil {
			t.Fatalf("probe %s: %v", tbl, err)
		}
		if reg == nil {
			t.Fatalf("表 %s 没建出来", tbl)
		}
	}

	if err := VerifySchema(ctx); err != nil {
		t.Fatalf("verify after migrate: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	setupScratch(t)
	ctx := context.Background()

	if err := Migrate(ctx, MigrateOptions{}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	var first time.Time
	if err := Pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = 1`).Scan(&first); err != nil {
		t.Fatalf("read applied_at: %v", err)
	}

	if err := Migrate(ctx, MigrateOptions{}); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var second time.Time
	var n int
	if err := Pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE version = 1`).Scan(&second); err != nil {
		t.Fatalf("read applied_at: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("版本行数 = %d, want 1", n)
	}
	if !first.Equal(second) {
		t.Fatalf("第二次 migrate 重写了 applied_at: %v → %v", first, second)
	}
}

// 这是最关键的一条：复刻「迁移改造前的存量生产库」，确认它在版本化之后
// 仍然能收敛，且业务数据一行不动。
func TestMigrateConvergesLegacyDatabase(t *testing.T) {
	setupScratch(t)
	ctx := context.Background()

	// 建到"改造前"的样子：有表、有列，但 cleanup 没跑过、约束没收紧
	// （team_id 允许 NULL，users.role 允许 0）。
	for _, st := range concatStmts(ddlStmts, backfillStmts) {
		if _, err := Pool.Exec(ctx, st.sql); err != nil {
			t.Fatalf("legacy setup: %v (sql=%s)", err, st.sql)
		}
	}

	if _, err := Pool.Exec(ctx, `INSERT INTO team (name) VALUES ('legacy-team')`); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	// 该被 cleanup 归属到最早创建的团队
	if _, err := Pool.Exec(ctx,
		`INSERT INTO model_route (model_pattern, route_type, team_id) VALUES ('gpt-legacy-*', 'openai', NULL)`); err != nil {
		t.Fatalf("seed model_route: %v", err)
	}
	// 该被 cleanup 删掉（team_id 为空 = 旧的「全局可见」语义）
	if _, err := Pool.Exec(ctx,
		`INSERT INTO exposed_model (model_id, team_id) VALUES ('ghost-model', NULL)`); err != nil {
		t.Fatalf("seed exposed_model(ghost): %v", err)
	}
	// 有归属，不该被动
	if _, err := Pool.Exec(ctx,
		`INSERT INTO exposed_model (model_id, team_id) VALUES ('kept-model', (SELECT id FROM team WHERE name = 'legacy-team'))`); err != nil {
		t.Fatalf("seed exposed_model(kept): %v", err)
	}
	// role=0 的历史用户，该被补成 root
	if _, err := Pool.Exec(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ('legacy-user', 'x', 0)`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// 业务数据，绝不能被动
	if _, err := Pool.Exec(ctx,
		`INSERT INTO api_logs (model, total_tokens) VALUES ('gpt-4', 42)`); err != nil {
		t.Fatalf("seed api_logs: %v", err)
	}

	// 前置：还没迁移过，且校验必须挡住启动
	st, err := ReadSchemaStatus(ctx)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if st.TableExists {
		t.Fatal("前置状态不对：这一步不该有 schema_migrations")
	}
	if err := VerifySchema(ctx); err == nil {
		t.Fatal("未迁移的库上 VerifySchema 必须报错")
	}

	// 真正的迁移
	if err := Migrate(ctx, MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	if err := VerifySchema(ctx); err != nil {
		t.Fatalf("verify after migrate: %v", err)
	}

	var teamID int
	if err := Pool.QueryRow(ctx, `SELECT id FROM team WHERE name = 'legacy-team'`).Scan(&teamID); err != nil {
		t.Fatalf("read team id: %v", err)
	}

	var routeTeam int
	if err := Pool.QueryRow(ctx, `SELECT team_id FROM model_route WHERE model_pattern = 'gpt-legacy-*'`).Scan(&routeTeam); err != nil {
		t.Fatalf("model_route.team_id 应该被 backfill 成非空: %v", err)
	}
	if routeTeam != teamID {
		t.Fatalf("model_route.team_id = %d, want %d（最早创建的团队）", routeTeam, teamID)
	}

	var ghosts, kept, logs int
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM exposed_model WHERE model_id = 'ghost-model'`).Scan(&ghosts); err != nil {
		t.Fatalf("count ghost: %v", err)
	}
	if ghosts != 0 {
		t.Fatal("team_id 为空的 exposed_model 应该被清理掉")
	}
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM exposed_model WHERE model_id = 'kept-model'`).Scan(&kept); err != nil {
		t.Fatalf("count kept: %v", err)
	}
	if kept != 1 {
		t.Fatal("有归属的 exposed_model 不该被删")
	}
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM api_logs`).Scan(&logs); err != nil {
		t.Fatalf("count api_logs: %v", err)
	}
	if logs != 1 {
		t.Fatalf("api_logs 行数 = %d, want 1 —— 迁移不该动业务数据", logs)
	}

	var role int
	if err := Pool.QueryRow(ctx, `SELECT role FROM users WHERE username = 'legacy-user'`).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != 1 {
		t.Fatalf("存量用户 role = %d, want 1", role)
	}

	// 约束确实收紧了（cleanup 跑在 constraints 之前，否则这里会失败）
	var nullable string
	if err := Pool.QueryRow(ctx,
		`SELECT is_nullable FROM information_schema.columns
		  WHERE table_name = 'model_route' AND column_name = 'team_id'`).Scan(&nullable); err != nil {
		t.Fatalf("read is_nullable: %v", err)
	}
	if nullable != "NO" {
		t.Fatalf("model_route.team_id is_nullable = %q, want NO", nullable)
	}
}

// 一个版本要么整体生效，要么整体回滚：失败的迁移不能留下半截结构，
// 也不能写版本行。
func TestMigrateRollsBackOnFailure(t *testing.T) {
	setupScratch(t)
	ctx := context.Background()

	// 临时往 v1 尾部塞「一条能建表的」+「一条必然报错的」，验证建表那条
	// 也被一起回滚掉。
	orig := migrations[0].stmts
	migrations[0].stmts = append(append([]statement{}, orig...),
		statement{sql: `CREATE TABLE rollback_probe (id INTEGER)`},
		statement{sql: `THIS IS NOT VALID SQL`},
	)
	defer func() { migrations[0].stmts = orig }()

	err := Migrate(ctx, MigrateOptions{})
	if err == nil {
		t.Fatal("含非法语句的迁移必须报错")
	}
	// 必须确认报的是「语句执行失败」而不是被前置校验提前挡掉 ——
	// 否则这个用例会在迁移根本没开始跑的情况下"通过"，回滚就没被验证。
	var mismatch *SchemaMismatchError
	if errors.As(err, &mismatch) {
		t.Fatalf("报的是版本校验失败，说明迁移没开始执行，回滚未被验证: %v", err)
	}
	if !strings.Contains(err.Error(), "statement") {
		t.Fatalf("错误应指出是第几条语句失败: %v", err)
	}

	var probe *string
	if err := Pool.QueryRow(ctx, `SELECT to_regclass('rollback_probe')::text`).Scan(&probe); err != nil {
		t.Fatalf("probe rollback_probe: %v", err)
	}
	if probe != nil {
		t.Fatal("事务已回滚，rollback_probe 不该存在")
	}

	st, err := ReadSchemaStatus(ctx)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	// schema_migrations 是迁移器在事务外建的，应该留着
	if !st.TableExists {
		t.Fatal("schema_migrations 不该被回滚掉（它在事务外创建）")
	}
	if len(st.Applied) != 0 {
		t.Fatalf("失败的迁移不该写版本行，实际 %d 条", len(st.Applied))
	}
	if len(st.Pending) != 1 || st.Pending[0] != 1 {
		t.Fatalf("pending = %v, want [1]", st.Pending)
	}
}

func TestMigrateConcurrentRunsSerialise(t *testing.T) {
	setupScratch(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = Migrate(ctx, MigrateOptions{})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发 migrate #%d: %v", i, err)
		}
	}
	var n int
	if err := Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("版本行数 = %d, want 1 —— advisory lock 没挡住重复应用", n)
	}
}

func TestVerifySchemaMatrix(t *testing.T) {
	setupScratch(t)
	ctx := context.Background()

	if err := Migrate(ctx, MigrateOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Run("已同步则通过", func(t *testing.T) {
		if err := VerifySchema(ctx); err != nil {
			t.Fatalf("verify: %v", err)
		}
	})

	t.Run("库超前只 WARN 不阻塞启动", func(t *testing.T) {
		if _, err := Pool.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, checksum) VALUES (99, 'from-the-future', 'x')`); err != nil {
			t.Fatalf("stamp future version: %v", err)
		}
		defer func() {
			_, _ = Pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 99`)
		}()

		if err := VerifySchema(ctx); err != nil {
			t.Fatalf("库比二进制新不该阻塞启动（回滚镜像要靠这条路）: %v", err)
		}
		st, err := ReadSchemaStatus(ctx)
		if err != nil {
			t.Fatalf("read status: %v", err)
		}
		if !st.Ahead {
			t.Fatal("Ahead 应为 true")
		}
	})

	t.Run("库缺版本则硬失败", func(t *testing.T) {
		if _, err := Pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 1`); err != nil {
			t.Fatalf("unstamp version: %v", err)
		}
		defer func() {
			// 重跑迁移把 v1 补回来（47 条语句幂等，等于顺带再验一次幂等性）
			if err := Migrate(ctx, MigrateOptions{}); err != nil {
				t.Fatalf("restore v1: %v", err)
			}
		}()

		err := VerifySchema(ctx)
		if err == nil {
			t.Fatal("库缺版本必须报错")
		}
		var mismatch *SchemaMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("错误类型 = %T, want *SchemaMismatchError", err)
		}
		if mismatch.Hint == "" {
			t.Fatal("SchemaMismatchError 必须给出 Hint（操作员要知道下一步做什么）")
		}
	})

	t.Run("checksum 漂移：启动只 WARN，migrate 致命", func(t *testing.T) {
		orig := migrations[0].stmts[0].sql
		// 加一段块注释：归一化之后仍是不同的 token 序列，能真正触发漂移。
		migrations[0].stmts[0].sql = orig + " /* drift */"
		defer func() { migrations[0].stmts[0].sql = orig }()

		if err := VerifySchema(ctx); err != nil {
			t.Fatalf("启动路径上 checksum 漂移只该 WARN（启动时没人能改库）: %v", err)
		}

		err := Migrate(ctx, MigrateOptions{})
		if err == nil {
			t.Fatal("migrate 路径上 checksum 漂移必须报错")
		}
		var mismatch *SchemaMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("错误类型 = %T, want *SchemaMismatchError", err)
		}
	})
}
