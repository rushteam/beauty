package pg

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- hashKey 单元测试 ----

func TestHashKey_Deterministic(t *testing.T) {
	d := NewDLock(nil)
	h1 := d.hashKey("foo")
	h2 := d.hashKey("foo")
	if h1 != h2 {
		t.Fatalf("hashKey not deterministic: %d != %d", h1, h2)
	}
}

func TestHashKey_DifferentKeys(t *testing.T) {
	d := NewDLock(nil)
	if d.hashKey("foo") == d.hashKey("bar") {
		t.Fatal("different keys should produce different hashes (collision)")
	}
}

func TestHashKey_PrefixIsolation(t *testing.T) {
	d1 := NewDLock(nil, WithPrefix("app1/"))
	d2 := NewDLock(nil, WithPrefix("app2/"))
	if d1.hashKey("leader") == d2.hashKey("leader") {
		t.Fatal("same key with different prefix should produce different hashes")
	}
}

// ---- Option 单元测试 ----

func TestOptions(t *testing.T) {
	d := NewDLock(nil,
		WithPrefix("test/"),
		WithRetryInterval(500*time.Millisecond),
		WithHeartbeat(3*time.Second),
	)
	if d.prefix != "test/" {
		t.Fatalf("prefix = %q, want %q", d.prefix, "test/")
	}
	if d.retry != 500*time.Millisecond {
		t.Fatalf("retry = %v, want 500ms", d.retry)
	}
	if d.heartbeat != 3*time.Second {
		t.Fatalf("heartbeat = %v, want 3s", d.heartbeat)
	}
}

func TestOptions_IgnoreZeroOrNegative(t *testing.T) {
	d := NewDLock(nil,
		WithPrefix(""),
		WithRetryInterval(-1),
		WithHeartbeat(0),
	)
	if d.prefix != defaultPrefix {
		t.Fatalf("empty prefix should be ignored, got %q", d.prefix)
	}
	if d.retry != defaultRetry {
		t.Fatalf("negative retry should be ignored, got %v", d.retry)
	}
	if d.heartbeat != defaultHeartbeat {
		t.Fatalf("zero heartbeat should be ignored, got %v", d.heartbeat)
	}
}

// ---- fake PG driver (for Lock/TryLock/Elector 流程测试) ----
//
// 用一个极简的 database/sql/driver 实现来模拟 pg_advisory_lock / pg_try_advisory_lock
// / pg_advisory_unlock,不依赖真实 PG 也不引入第三方 mock 库。

var fakeDriverName = "fake_pg_advisory"

func init() {
	sql.Register(fakeDriverName, &fakeDriver{})
}

type fakeDriver struct{}

func (d *fakeDriver) Open(_ string) (driver.Conn, error) {
	return newFakeConn(), nil
}

// fakeLockManager 进程内模拟 advisory lock:key → 持有该锁的 conn 数量(简化为 bool)。
var fakeLockMgr = struct {
	mu    sync.Mutex
	locks map[int64]*fakeConn
}{locks: make(map[int64]*fakeConn)}

type fakeConn struct {
	mu     sync.Mutex
	closed bool
	held   map[int64]bool // 本连接持有的 lock keys
}

func newFakeConn() *fakeConn {
	return &fakeConn{held: make(map[int64]bool)}
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{conn: c, query: query}, nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	held := make([]int64, 0, len(c.held))
	for k := range c.held {
		held = append(held, k)
	}
	c.held = nil
	c.mu.Unlock()

	fakeLockMgr.mu.Lock()
	for _, k := range held {
		if fakeLockMgr.locks[k] == c {
			delete(fakeLockMgr.locks, k)
		}
	}
	fakeLockMgr.mu.Unlock()
	return nil
}

func (c *fakeConn) Begin() (driver.Tx, error) { return &fakeTx{}, nil }

func (c *fakeConn) Ping(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("connection closed")
	}
	return nil
}

type fakeTx struct{}

func (t *fakeTx) Commit() error   { return nil }
func (t *fakeTx) Rollback() error { return nil }

type fakeStmt struct {
	conn  *fakeConn
	query string
}

func (s *fakeStmt) Close() error                               { return nil }
func (s *fakeStmt) NumInput() int                               { return 1 }
func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) { return fakeResult{}, nil }

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("expected 1 arg")
	}
	key := args[0].(int64)

	switch {
	case contains(s.query, "pg_advisory_lock"):
		// 阻塞版:无条件获取(在 fake 中直接获取,不阻塞)
		fakeLockMgr.mu.Lock()
		fakeLockMgr.locks[key] = s.conn
		fakeLockMgr.mu.Unlock()
		s.conn.mu.Lock()
		s.conn.held[key] = true
		s.conn.mu.Unlock()
		return &fakeRows{val: ""}, nil // pg_advisory_lock 返回 void

	case contains(s.query, "pg_try_advisory_lock"):
		fakeLockMgr.mu.Lock()
		holder, ok := fakeLockMgr.locks[key]
		if !ok || holder == s.conn {
			fakeLockMgr.locks[key] = s.conn
			fakeLockMgr.mu.Unlock()
			s.conn.mu.Lock()
			s.conn.held[key] = true
			s.conn.mu.Unlock()
			return &fakeRows{val: true}, nil
		}
		fakeLockMgr.mu.Unlock()
		return &fakeRows{val: false}, nil

	case contains(s.query, "pg_advisory_unlock"):
		fakeLockMgr.mu.Lock()
		if fakeLockMgr.locks[key] == s.conn {
			delete(fakeLockMgr.locks, key)
		}
		fakeLockMgr.mu.Unlock()
		s.conn.mu.Lock()
		delete(s.conn.held, key)
		s.conn.mu.Unlock()
		return &fakeRows{val: true}, nil
	}

	return nil, fmt.Errorf("unhandled query: %s", s.query)
}

type fakeRows struct {
	val    interface{}
	called bool
}

func (r *fakeRows) Columns() []string { return []string{"result"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.called {
		return io.EOF
	}
	r.called = true
	dest[0] = r.val
	return nil
}

type fakeResult struct{}

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return 0, nil }

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func openFakeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(fakeDriverName, "fake")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---- Lock/TryLock/Unlock 测试 ----

func TestLock_AndUnlock(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db)

	ctx := context.Background()
	lock, err := d.Lock(ctx, "test-lock")
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	// 二次 Unlock 幂等
	if err := lock.Unlock(ctx); err != nil {
		t.Fatalf("Unlock(2nd): %v", err)
	}
}

func TestTryLock_Acquired(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db)

	ctx := context.Background()
	lock, ok, err := d.TryLock(ctx, "trylock-key")
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !ok {
		t.Fatal("expected lock acquired")
	}
	lock.Unlock(ctx)
}

func TestTryLock_AlreadyHeld(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db)

	ctx := context.Background()
	lock1, ok, err := d.TryLock(ctx, "contended")
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v, err=%v", ok, err)
	}
	defer lock1.Unlock(ctx)

	// 第二个 TryLock 应该失败(不同连接)
	_, ok2, err2 := d.TryLock(ctx, "contended")
	if err2 != nil {
		t.Fatalf("second TryLock err: %v", err2)
	}
	if ok2 {
		t.Fatal("expected second TryLock to fail (lock held by another conn)")
	}
}

func TestLock_ContextCancel(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.Lock(ctx, "cancelled")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

// ---- Elector 测试 ----

func TestElector_BecomesLeader(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db, WithRetryInterval(10*time.Millisecond), WithHeartbeat(50*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	elected := make(chan struct{})
	go d.Run(ctx, "leader-key", func(leaderCtx context.Context) {
		close(elected)
		<-leaderCtx.Done()
	})

	select {
	case <-elected:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("onElected was never called")
	}
}

func TestElector_LeaderCtxCancelledOnOuterCancel(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db, WithRetryInterval(10*time.Millisecond), WithHeartbeat(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	elected := make(chan context.Context, 1)
	done := make(chan error, 1)

	go func() {
		done <- d.Run(ctx, "leader-cancel", func(leaderCtx context.Context) {
			elected <- leaderCtx
			<-leaderCtx.Done()
		})
	}()

	var leaderCtx context.Context
	select {
	case leaderCtx = <-elected:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("never elected")
	}

	cancel()

	select {
	case <-leaderCtx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("leaderCtx should be cancelled after outer ctx cancel")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after outer ctx cancel")
	}
}

func TestElector_ReelectsAfterCallbackReturns(t *testing.T) {
	db := openFakeDB(t)
	d := NewDLock(db, WithRetryInterval(10*time.Millisecond), WithHeartbeat(50*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var count atomic.Int32
	go d.Run(ctx, "reelect-key", func(leaderCtx context.Context) {
		count.Add(1)
	})

	time.Sleep(300 * time.Millisecond)
	if n := count.Load(); n < 2 {
		t.Fatalf("expected at least 2 elections, got %d", n)
	}
}
