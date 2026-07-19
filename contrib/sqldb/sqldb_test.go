package sqldb_test

import (
	"context"
	"testing"

	_ "modernc.org/sqlite" // 注册纯 Go 的 "sqlite" 驱动(免 cgo)

	"github.com/rushteam/beauty/contrib/sqldb"
)

// 两个独立的 :memory: 库(主/副各一个),分别塞入不同数据以观测路由落到了哪。
func openSplit(t *testing.T) *sqldb.DB {
	t.Helper()
	db, err := sqldb.Open(sqldb.Config{
		Driver:       "sqlite",
		PrimaryDSN:   ":memory:",
		ReplicaDSNs:  []string{":memory:"},
		MaxOpenConns: 1, // :memory: 单连接,保证 schema/数据在同一库内可见
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	exec(t, db.Writer(), "CREATE TABLE t(v TEXT)")
	exec(t, db.Writer(), "INSERT INTO t(v) VALUES('P')") // 主库标记 P
	exec(t, db.Reader(), "CREATE TABLE t(v TEXT)")
	exec(t, db.Reader(), "INSERT INTO t(v) VALUES('R')") // 副本标记 R
	return db
}

func exec(t *testing.T, d sqldb.DBTX, q string) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func scan(t *testing.T, d sqldb.DBTX, ctx context.Context) string {
	t.Helper()
	var v string
	if err := d.QueryRowContext(ctx, "SELECT v FROM t LIMIT 1").Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	return v
}

// 显式句柄:Writer 命中主库、Reader 命中副本。
func TestWriterReader_Split(t *testing.T) {
	db := openSplit(t)
	defer db.Close()
	if got := scan(t, db.Writer(), context.Background()); got != "P" {
		t.Fatalf("Writer 读到 %q, want P(主库)", got)
	}
	if got := scan(t, db.Reader(), context.Background()); got != "R" {
		t.Fatalf("Reader 读到 %q, want R(副本)", got)
	}
}

// 自动路由:Query 默认走副本;Primary(ctx) 强制走主库;Exec 落主库。
func TestRW_AutoRoute(t *testing.T) {
	db := openSplit(t)
	defer db.Close()
	rw := db.RW()
	ctx := context.Background()

	if got := scan(t, rw, ctx); got != "R" {
		t.Fatalf("RW 读默认应走副本, got %q want R", got)
	}
	if got := scan(t, rw, sqldb.Primary(ctx)); got != "P" {
		t.Fatalf("RW + Primary(ctx) 应走主库, got %q want P", got)
	}

	// Exec 走主库:插入后主库两行、副本仍一行。
	exec(t, rw, "INSERT INTO t(v) VALUES('X')")
	if n := count(t, db.Writer()); n != 2 {
		t.Fatalf("Exec 后主库行数 = %d, want 2", n)
	}
	if n := count(t, db.Reader()); n != 1 {
		t.Fatalf("副本行数应不变 = %d, want 1", n)
	}
}

// RW 自动嗅探:INSERT...RETURNING(走 QueryRow 却是写)应被自动路由到主库,而非副本。
func TestRW_ReturningRoutesToPrimary(t *testing.T) {
	db := openSplit(t) // 主库有 'P',副本有 'R'
	defer db.Close()
	rw := db.RW()

	var got string
	// 未加任何 Primary(ctx) 提示,靠意图嗅探判定这是写 → 落主库。
	err := rw.QueryRowContext(context.Background(), "INSERT INTO t(v) VALUES('Y') RETURNING v").Scan(&got)
	if err != nil {
		t.Fatalf("returning: %v", err)
	}
	if got != "Y" {
		t.Fatalf("returning 值 = %q, want Y", got)
	}
	if n := count(t, db.Writer()); n != 2 {
		t.Fatalf("主库应有 2 行(P+Y),得 %d —— RETURNING 未落主库", n)
	}
	if n := count(t, db.Reader()); n != 1 {
		t.Fatalf("副本应仍 1 行(R),得 %d —— 写误落副本", n)
	}
}

// 无副本时读回退主库。
func TestNoReplica_FallbackToPrimary(t *testing.T) {
	db, err := sqldb.Open(sqldb.Config{
		Driver: "sqlite", PrimaryDSN: ":memory:", MaxOpenConns: 1, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	exec(t, db.Writer(), "CREATE TABLE t(v TEXT)")
	exec(t, db.Writer(), "INSERT INTO t(v) VALUES('P')")
	if got := scan(t, db.Reader(), context.Background()); got != "P" {
		t.Fatalf("无副本时 Reader 应回退主库, got %q want P", got)
	}
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func count(t *testing.T, d sqldb.DBTX) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
