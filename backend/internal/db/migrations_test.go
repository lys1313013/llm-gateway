package db

import (
	"regexp"
	"strings"
	"testing"
)

// baselineStatementCount 是 version 1 的语句条数，刻意钉死。
//
// 它是 canary：从 47 条里少掉一条（比如误删某个 backfill）在**空库**上跑不会
// 报任何错，只会让存量库少补一列，等线上查询 500 才发现。所以增删语句时
// 必须同步改这个数 —— 改不动就说明你并不想改语句集。
const baselineStatementCount = 47

func TestBaselineStatementCount(t *testing.T) {
	if got := len(baselineStmts); got != baselineStatementCount {
		t.Fatalf("baselineStmts 有 %d 条，期望 %d：是有意增删语句吗？"+
			"是就把 baselineStatementCount 一起改掉", got, baselineStatementCount)
	}
}

// 版本号必须从 1 起连续：Migrate 靠「库里没有 = 待应用」来决定跑哪些版本，
// 中间断号会让断号之后的版本永远跑不到。
func TestMigrationsContiguousFromOne(t *testing.T) {
	if len(migrations) == 0 {
		t.Fatal("migrations 为空")
	}
	for i, m := range migrations {
		if m.version != i+1 {
			t.Fatalf("第 %d 个迁移的 version = %d，期望 %d（必须从 1 起连续）",
				i, m.version, i+1)
		}
		if m.name == "" {
			t.Fatalf("v%d 没有 name", m.version)
		}
		if len(m.stmts) == 0 {
			t.Fatalf("v%d 没有语句", m.version)
		}
	}
}

// 每个迁移就是一个事务，所以语句集里不能出现只能在事务外执行的东西。
// 这条断言是 applyMigration 那个设计的**前提**，不是可选检查。
func TestMigrationsHaveNoTxIncompatibleSQL(t *testing.T) {
	re := regexp.MustCompile(
		`(?i)\bCONCURRENTLY\b|\bVACUUM\b|\bCREATE\s+DATABASE\b|\bDROP\s+DATABASE\b|\bALTER\s+SYSTEM\b`)
	for _, m := range migrations {
		for i, st := range m.stmts {
			if loc := re.FindString(st.sql); loc != "" {
				t.Fatalf("v%d 第 %d 条语句含 %q：它不能跑在事务里，而每个迁移就是一个事务",
					m.version, i+1, loc)
			}
		}
	}
}

// 一次性数据清理必须带 warn 文案：它们正常情况下影响 0 行，
// 影响到行就说明这个库确实带着旧数据，必须在日志里留痕。
func TestCleanupStatementsAllCarryWarnText(t *testing.T) {
	if len(cleanupStmts) == 0 {
		t.Fatal("cleanupStmts 为空")
	}
	for i, st := range cleanupStmts {
		if strings.TrimSpace(st.warn) == "" {
			t.Fatalf("cleanupStmts[%d] 没写 warn 文案", i)
		}
	}
}

func TestChecksumShape(t *testing.T) {
	sum := migrations[0].Checksum()
	if len(sum) != 64 {
		t.Fatalf("checksum = %q（%d 位），期望 sha256 的 64 位十六进制", sum, len(sum))
	}
}

// 重排缩进/换行是纯排版改动，不该让已应用的迁移"漂移"。
func TestChecksumNormalizesWhitespace(t *testing.T) {
	a := migration{version: 1, stmts: []statement{
		{sql: "CREATE TABLE t (\n\tid INTEGER,\n\tname TEXT\n)"},
	}}
	b := migration{version: 1, stmts: []statement{
		{sql: "CREATE TABLE t ( id INTEGER, name TEXT )"},
	}}
	if a.Checksum() != b.Checksum() {
		t.Fatal("只重排缩进/换行改变了 checksum")
	}
}

func TestChecksumDetectsSQLChange(t *testing.T) {
	a := migration{version: 1, stmts: []statement{{sql: "SELECT 1"}}}
	b := migration{version: 1, stmts: []statement{{sql: "SELECT 2"}}}
	if a.Checksum() == b.Checksum() {
		t.Fatal("SQL 文本变了 checksum 没变")
	}
}

// 语句顺序也是语义的一部分，重排必须被发现。
func TestChecksumIsOrderSensitive(t *testing.T) {
	a := migration{version: 1, stmts: []statement{{sql: "SELECT 1"}, {sql: "SELECT 2"}}}
	b := migration{version: 1, stmts: []statement{{sql: "SELECT 2"}, {sql: "SELECT 1"}}}
	if a.Checksum() == b.Checksum() {
		t.Fatal("语句顺序变了 checksum 没变")
	}
}

// warn 只影响日志，不参与 checksum —— 否则改一句措辞就要重新盖章。
func TestChecksumIgnoresWarnText(t *testing.T) {
	a := migration{version: 1, stmts: []statement{{sql: "SELECT 1", warn: "旧文案"}}}
	b := migration{version: 1, stmts: []statement{{sql: "SELECT 1", warn: "新文案"}}}
	if a.Checksum() != b.Checksum() {
		t.Fatal("改 WARN 文案触发了 checksum 漂移")
	}
}

func TestLatestVersion(t *testing.T) {
	if got := LatestVersion(); got != migrations[len(migrations)-1].version {
		t.Fatalf("LatestVersion() = %d, want %d", got, migrations[len(migrations)-1].version)
	}
}
