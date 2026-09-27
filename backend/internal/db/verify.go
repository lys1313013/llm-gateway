package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// migrationsTable 是迁移器的版本表，由迁移器自己 CREATE TABLE IF NOT EXISTS。
// 因此「表不存在」就是「这个库从未迁移过」的判据。
const migrationsTable = "schema_migrations"

// MigrationRecord 是 schema_migrations 里的一行。
type MigrationRecord struct {
	Version     int
	Name        string
	Checksum    string
	AppliedAt   time.Time
	ExecutionMS *int
	AppVersion  *string
}

// SchemaStatus 是「库里的版本状态」与「二进制期望的版本状态」的比对结果。
type SchemaStatus struct {
	// TableExists 为 false 时其余字段都是零值：库从未迁移过。
	TableExists bool
	Applied     []MigrationRecord
	// DBVersion 是库里最大的版本号，没迁移过时为 0。
	DBVersion int
	// Pending 是二进制里有、库里没有的版本，升序。落后和「中间缺版本」
	// 都落在这一项上。
	Pending []int
	// ChecksumDrift 是「库里有这个版本，但记录的 checksum 与二进制当前文本
	// 不符」——即已发布的迁移被人改过。
	ChecksumDrift []int
	// Ahead 表示库里的版本比二进制还新（镜像过旧 / 回滚场景）。
	Ahead bool
}

// SchemaMismatchError 表示库的 schema 与二进制不匹配，且属于「不能继续」的
// 那一类：从未迁移、库缺版本、或已发布迁移的 SQL 文本被改动过。
// Hint 是给操作员的下一步动作。
type SchemaMismatchError struct {
	Msg  string
	Hint string
}

func (e *SchemaMismatchError) Error() string { return e.Msg }

// querier 让同一份读取逻辑既能走连接池也能走迁移时那条专用连接
// （后者是为了避免读到别的连接上的中间态）。
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// VerifySchema 只读校验库里的版本与二进制是否匹配。服务启动路径经 Init
// 调用它；测试也可以单独调。
//
// 两档处理：
//   - 从未迁移 / 库缺版本 → 硬失败（verifyInSync）。这是这套东西的核心价值：
//     防止旧代码读到没迁移的 schema，运行期每个查询 500。
//   - 库比二进制新 / 已应用迁移的文本被改动 → 只 WARN 并继续启动
//     （warnIfAheadOrDrifted）。启动路径上没有人能改库，把线上卡死在这里没有
//     收益；而且「库比二进制新」正是回滚镜像时的正常状态。
//     注意 checksum 漂移在 **Migrate** 路径上是致命的 —— 那边执行迁移前必须
//     确认库与二进制一致（见 checkChecksums），那是操作员在场、能处理的时刻。
func VerifySchema(ctx context.Context) error {
	st, err := ReadSchemaStatus(ctx)
	if err != nil {
		return err
	}
	if err := verifyInSync(st); err != nil {
		return err
	}
	warnIfAheadOrDrifted(st)
	return nil
}

// ReadSchemaStatus 只读地取出当前状态。不建表、不加锁。
func ReadSchemaStatus(ctx context.Context) (*SchemaStatus, error) {
	if Pool == nil {
		return nil, fmt.Errorf("db: ReadSchemaStatus called before Connect")
	}
	return readStatusFrom(ctx, Pool)
}

// verifyInSync 断言库与二进制**完全一致**：没有从未迁移，也没有缺版本。
// 启动路径和迁移收尾都用它。
//
// 刻意不包含 checksum 漂移 —— 那属于 warnIfAheadOrDrifted / checkChecksums
// 的职责，两条路径对它的处置不同。
func verifyInSync(st *SchemaStatus) error {
	binaryMax := latestVersion()

	if !st.TableExists {
		return &SchemaMismatchError{
			Msg:  "schema 从未迁移（" + migrationsTable + " 表不存在）",
			Hint: "先跑 `gateway migrate`",
		}
	}

	if len(st.Pending) > 0 {
		msg := fmt.Sprintf("库缺失迁移版本 %v（库 v%d）", st.Pending, st.DBVersion)
		if st.DBVersion < binaryMax {
			msg = fmt.Sprintf("schema 落后：库是 v%d，二进制期望 v%d（缺失 %v）",
				st.DBVersion, binaryMax, st.Pending)
		}
		return &SchemaMismatchError{Msg: msg, Hint: "先跑 `gateway migrate`"}
	}

	return nil
}

// warnIfAheadOrDrifted 处理两条「不阻塞」的情况，只打日志。
//
// 都不返回错误是刻意的：
//   - 库比二进制新，正是回滚镜像时的状态，硬失败会让回滚这条路直接走不通。
//   - checksum 漂移在启动路径上没人能处理（启动时也没人在改库），在那里卡死
//     线上没有收益；Migrate 路径会把它升级为错误（checkChecksums）。
func warnIfAheadOrDrifted(st *SchemaStatus) {
	if st.Ahead {
		// 措辞要同时适用于启动和 migrate 两条路径（这个函数两边都调）。
		slog.Warn("schema 比二进制新（镜像可能过旧）；不做处理，以便回滚镜像这条路走得通",
			"db_version", st.DBVersion, "binary_version", latestVersion())
	}
	if len(st.ChecksumDrift) > 0 {
		slog.Warn("已应用的迁移 SQL 文本与当前二进制不一致（可能被改动过）",
			"versions", fmt.Sprint(st.ChecksumDrift))
	}
}

// checkChecksums 是 **Migrate 专用**的前置硬校验。
//
// 它刻意**不**检查「库缺版本」：那正是迁移要解决的事情。只拦 checksum 漂移 ——
// 已应用的迁移文本被改过，意味着「库现在到底长什么样」是未知的，此时继续往上
// 叠新版本，结果无法预期。让操作员先把误改的 SQL 改回去。
func checkChecksums(st *SchemaStatus) error {
	if len(st.ChecksumDrift) > 0 {
		return &SchemaMismatchError{
			Msg:  fmt.Sprintf("已应用的迁移 SQL 文本被改动过：版本 %v", st.ChecksumDrift),
			Hint: "把改动的 SQL 改回原样——改已发布的迁移不会重新作用到库上，只会让这条防线失效",
		}
	}
	// 断号的版本（缺的比库里最新的还旧）正常不该出现，多半是有人手删过版本行。
	// 仍然补跑，但要说一声。
	for _, v := range st.Pending {
		if v < st.DBVersion {
			slog.Warn("库里缺的是中间版本，将补跑（正常不该出现，检查是否有人手删过版本行）",
				"missing_version", v, "db_version", st.DBVersion)
		}
	}
	return nil
}

// readStatusFrom 从给定连接读取状态并算出与二进制的差异。
func readStatusFrom(ctx context.Context, q querier) (*SchemaStatus, error) {
	st := &SchemaStatus{}

	// to_regclass 不报错：目标不存在时返回 NULL。刻意不用
	// `SELECT ... FROM schema_migrations` 再捕获错误——那会把「表不存在」
	// 和「连接坏了」混成同一类，而前者是正常的初始状态。
	var reg *string
	if err := q.QueryRow(ctx, `SELECT to_regclass($1)::text`, migrationsTable).Scan(&reg); err != nil {
		return nil, fmt.Errorf("probe %s: %w", migrationsTable, err)
	}
	if reg == nil {
		return st, nil
	}
	st.TableExists = true

	rows, err := q.Query(ctx,
		`SELECT version, name, checksum, applied_at, execution_ms, app_version
		   FROM `+migrationsTable+` ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", migrationsTable, err)
	}
	defer rows.Close()
	for rows.Next() {
		var r MigrationRecord
		if err := rows.Scan(&r.Version, &r.Name, &r.Checksum, &r.AppliedAt,
			&r.ExecutionMS, &r.AppVersion); err != nil {
			return nil, fmt.Errorf("scan %s: %w", migrationsTable, err)
		}
		st.Applied = append(st.Applied, r)
		if r.Version > st.DBVersion {
			st.DBVersion = r.Version
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", migrationsTable, err)
	}

	applied := make(map[int]string, len(st.Applied))
	for _, r := range st.Applied {
		applied[r.Version] = r.Checksum
	}
	for _, m := range migrations {
		sum, ok := applied[m.version]
		if !ok {
			st.Pending = append(st.Pending, m.version)
			continue
		}
		if sum != m.Checksum() {
			st.ChecksumDrift = append(st.ChecksumDrift, m.version)
		}
	}
	st.Ahead = st.DBVersion > latestVersion()

	return st, nil
}
