package db

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lys1313013/llm-gateway/backend/internal/config"
)

// migrateLockKey 是迁移用的 advisory lock 键，取一个固定值即可，只要全项目一致。
//
// 关键约束：pg_advisory_lock 是 **session 级**的，而 Pool.Exec 每次可能落到不同
// 连接上——所以加锁、执行迁移、写版本行、解锁必须在同一条 Pool.Acquire 出来的
// 连接上完成。锁没放掉就把连接还回池里，等于把迁移永久阻塞住：pgxpool 的
// Release 不释放 session 级锁，那条连接之后谁 Acquire 到谁就白等。
const migrateLockKey int64 = 0x6c6c6d675f6d6967 // "llmg_mig"

const (
	// 等锁的轮询节奏；总预算复用 DB_CONNECT_TIMEOUT。
	migrateLockPollInterval = 500 * time.Millisecond
	// 解锁用的超时，配独立的 context —— 请求 ctx 可能已经取消了。
	migrateUnlockTimeout = 5 * time.Second
	// 迁移事务内的 lock_timeout：拿不到表锁就报错回滚，而不是静默挂死。
	// 用 set_config(..., is_local=true)，事务结束自动复位，不污染该连接后续会话。
	migrateLockTimeout = "10s"
)

// MigrateOptions 是 Migrate 的入参。
type MigrateOptions struct {
	// AppVersion 记进 schema_migrations.app_version，用来回答「这套结构是哪个
	// 镜像改的」。传编译期注入的版本号即可，可以留空。
	AppVersion string
}

// Migrate 应用所有缺失的版本。可重复执行：已是最新时不做任何事。
//
// Pool 还没建时先自行 Connect —— 迁移子命令走的是这条路径（它不能用 Init，
// 因为 Init 会先校验版本，而未迁移的库恰好校验不过）。
func Migrate(ctx context.Context, opts MigrateOptions) error {
	if Pool == nil {
		if err := Connect(ctx); err != nil {
			return err
		}
	}

	conn, err := Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	// defer 是 LIFO：先注册 Release 再注册 unlock，于是 unlock 先跑、Release
	// 后跑。锁必须在连接归还池之前放掉，顺序反了就会把带锁的连接还回去。
	defer conn.Release()
	if err := lockMigration(ctx, conn); err != nil {
		return err
	}
	defer unlockMigration(conn)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTable+` (
		version      INTEGER PRIMARY KEY,
		name         VARCHAR(128) NOT NULL,
		checksum     CHAR(64) NOT NULL,
		applied_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		execution_ms INTEGER,
		app_version  VARCHAR(64)
	)`); err != nil {
		return fmt.Errorf("create %s: %w", migrationsTable, err)
	}

	// 用同一条连接读状态，避免读到别的连接上的未提交中间态。
	before, err := readStatusFrom(ctx, conn)
	if err != nil {
		return err
	}
	// 前置只拦 checksum 漂移；「库缺版本」在这里是正常状态，正是要补的东西。
	if err := checkChecksums(before); err != nil {
		return err
	}
	warnIfAheadOrDrifted(before)
	if len(before.Pending) == 0 {
		slog.Info("schema 已是最新，无需迁移",
			"version", before.DBVersion, "binary_version", latestVersion())
		return nil
	}

	slog.Info("开始迁移",
		"from_version", before.DBVersion, "to_version", latestVersion(),
		"pending", fmt.Sprint(before.Pending))

	for _, m := range migrations {
		if !slices.Contains(before.Pending, m.version) {
			continue
		}
		if err := applyMigration(ctx, conn, m, opts.AppVersion); err != nil {
			return fmt.Errorf("apply migration v%d (%s): %w", m.version, m.name, err)
		}
	}

	// 收尾再读一次：确认库里落下来的东西二进制自己认得（既不该再有 pending，
	// 也不该出现漂移）。
	after, err := readStatusFrom(ctx, conn)
	if err != nil {
		return err
	}
	if err := verifyInSync(after); err != nil {
		return err
	}
	warnIfAheadOrDrifted(after)
	return nil
}

// applyMigration 在**一个事务**里跑完一个版本的全部语句并写下版本行。
// 失败则整个事务回滚、不写版本行，下次重跑从同一版本开始，不会留半截状态。
//
// 逐版本一个事务而不是整批一个：现在只有一个版本无所谓，但将来某个版本要建
// 索引（短暂 ACCESS EXCLUSIVE）时，不该被前面所有版本的持锁时间叠加。
// 这套成立的前提是 PostgreSQL 的 DDL 是事务性的 —— 迁移集里不得出现
// CREATE INDEX CONCURRENTLY / VACUUM（有测试拦）。
func applyMigration(ctx context.Context, conn *pgxpool.Conn, m migration, appVersion string) error {
	start := time.Now()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// rollback 用 Background：ctx 可能已经取消，但事务必须收掉，否则这条连接
	// 会停在 'T' 状态，Release 时被判定为脏连接直接销毁。
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, migrateLockTimeout); err != nil {
		return fmt.Errorf("set lock_timeout: %w", err)
	}

	for i, st := range m.stmts {
		tag, err := tx.Exec(ctx, st.sql)
		if err != nil {
			return fmt.Errorf("statement %d/%d: %w (sql=%s)", i+1, len(m.stmts), err, st.sql)
		}
		if st.warn != "" {
			if n := tag.RowsAffected(); n > 0 {
				slog.Warn("migration: "+st.warn, "version", m.version, "rows", n)
			}
		}
	}

	// execution_ms 是语句执行时间，不含 COMMIT（事务性 DDL 的提交本身极快）。
	ms := int(time.Since(start).Milliseconds())
	if _, err := tx.Exec(ctx,
		`INSERT INTO `+migrationsTable+` (version, name, checksum, execution_ms, app_version)
		 VALUES ($1, $2, $3, $4, $5)`,
		m.version, m.name, m.Checksum(), ms, nullableStr(appVersion)); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("migration applied",
		"version", m.version, "name", m.name,
		"statements", len(m.stmts), "execution_ms", ms)
	return nil
}

// lockMigration 用 pg_try_advisory_lock 轮询取锁，而不是阻塞版的
// pg_advisory_lock：这样「等锁」也受超时预算约束，超时能给出可读的报错，
// 而不是进程毫无输出地挂在那里。
func lockMigration(ctx context.Context, conn *pgxpool.Conn) error {
	budget := time.Duration(config.Get().DBConnectTimeoutS) * time.Second
	if budget <= 0 {
		budget = 60 * time.Second
	}
	deadline := time.Now().Add(budget)

	for {
		var got bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, migrateLockKey).Scan(&got); err != nil {
			return fmt.Errorf("try advisory lock: %w", err)
		}
		if got {
			return nil
		}
		if !time.Now().Add(migrateLockPollInterval).Before(deadline) {
			return fmt.Errorf("等迁移锁超时（%s 内未拿到）：另一个 migrate 还在跑？", budget)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等迁移锁: %w", ctx.Err())
		case <-time.After(migrateLockPollInterval):
		}
	}
}

// unlockMigration 放锁。解锁失败说明这条连接已经不可信：Hijack 把它从池里
// 摘走并关掉 —— 连接关闭时 PostgreSQL 会释放它的 session 级锁。不能只 Release
// 了事，那会把一条带着锁的连接还回池中，之后谁 Acquire 到谁就白等。
//
// pgxpool.Conn.Hijack 在连接已 Release 的情况下会 panic，所以它的调用必须
// 早于同作用域里的 conn.Release()（靠 defer LIFO 保证，见 Migrate）。
func unlockMigration(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), migrateUnlockTimeout)
	defer cancel()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, migrateLockKey); err != nil {
		slog.Warn("释放迁移锁失败，丢弃这条连接以便 PostgreSQL 释放 session 级锁", "err", err)
		_ = conn.Hijack().Close(context.Background())
	}
}

// nullableStr 把空串写成 SQL NULL：app_version 留空时不该存空字符串，否则
// 和「版本号恰好是空的」混在一起。
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
