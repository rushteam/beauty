package mysql

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

// ---- lockName 单元测试 ----

func TestLockName_Short(t *testing.T) {
	d := NewDLock(nil)
	name := d.lockName("foo")
	want := defaultPrefix + "foo"
	if name != want {
		t.Fatalf("lockName = %q, want %q", name, want)
	}
}

func TestLockName_Deterministic(t *testing.T) {
	d := NewDLock(nil)
	n1 := d.lockName("foo")
	n2 := d.lockName("foo")
	if n1 != n2 {
		t.Fatalf("lockName not deterministic: %q != %q", n1, n2)
	}
}

func TestLockName_DifferentKeys(t *testing.T) {
	d := NewDLock(nil)
	if d.lockName("foo") == d.lockName("bar") {
		t.Fatal("different keys should produce different lock names")
	}
}

func TestLockName_PrefixIsolation(t *testing.T) {
	d1 := NewDLock(nil, WithPrefix("app1:"))
	d2 := NewDLock(nil, WithPrefix("app2:"))
	if d1.lockName("leader") == d2.lockName("leader") {
		t.Fatal("same key with different prefix should produce different lock names")
	}
}

func TestLockName_LongKeyHashed(t *testing.T) {
	d := NewDLock(nil)
	longKey := string(make([]byte, 100)) // 100 chars of '\x00'
	name := d.lockName(longKey)
	if len(name) > maxLockNameLen {
		t.Fatalf("lockName length = %d, exceeds MySQL limit %d", len(name), maxLockNameLen)
	}
	if len(name) != 16 {
		t.Fatalf("expected 16-char hex hash, got %d chars: %q", len(name), name)
	}
}

// ---- Option 单元测试 ----

func TestOptions(t *testing.T) {
	d := NewDLock(nil,
		WithPrefix("test:"),
		WithRetryInterval(500*time.Millisecond),
		WithHeartbeat(3*time.Second),
	)
	if d.prefix != "test:" {
		t.Fatalf("prefix = %q, want %q", d.prefix, "test:")
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

// ---- fake MySQL driver（模拟 GET_LOCK / RELEASE_LOCK） ----
//
// 用极简的 database/sql/driver 实现模拟 MySQL advisory lock 语义，
// 不依赖真实 MySQL 也不引入第三方 mock 库。

var fakeDriverName = "fake_mysql_advisory"

func init() {
	sql.Register(fakeDriverName, &fakeDriver{})
}

type fakeDriver struct{}

func (d *fakeDriver) Open(_ string) (driver.Conn, error) {
	return newFakeConn(), nil
}

// fakeLockManager 进程内模拟 advisory lock：name → 持有该锁的 conn。
var fakeLockMgr = struct {
	mu    sync.Mutex
	locks map[string]*fakeConn
}{locks: make(map[string]*fakeConn)}

type fakeConn struct {
	mu     sync.Mutex
	closed bool
	held   map[string]bool
}

func newFakeConn() *fakeConn {
	return &fakeConn{held: make(map[string]bool)}
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{conn: c, query: query}, nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	held := make([]string, 0, len(c.held))
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

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 } // variable args

func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	if contains(s.query, "RELEASE_LOCK") && len(args) >= 1 {
		name := args[0].(string)
		fakeLockMgr.mu.Lock()
		if fakeLockMgr.locks[name] == s.conn {
			delete(fakeLockMgr.locks, name)
		}
		fakeLockMgr.mu.Unlock()
		s.conn.mu.Lock()
		delete(s.conn.held, name)
		s.conn.mu.Unlock()
	}
	return fakeResult{}, nil
}

func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("expected at least 1 arg")
	}
	name := args[0].(string)

	switch {
	case contains(s.query, "GET_LOCK"):
		fakeLockMgr.mu.Lock()
		holder, ok := fakeLockMgr.locks[name]
		if !ok || holder == s.conn {
			fakeLockMgr.locks[name] = s.conn
			fakeLockMgr.mu.Unlock()
			s.conn.mu.Lock()
			s.conn.held[name] = true
			s.conn.mu.Unlock()
			return &fakeRows{val: int64(1)}, nil
		}
		fakeLockMgr.mu.Unlock()
		return &fakeRows{val: int64(0)}, nil

	case contains(s.query, "RELEASE_LOCK"):
		fakeLockMgr.mu.Lock()
		if fakeLockMgr.locks[name] == s.conn {
			delete(fakeLockMgr.locks, name)
		}
		fakeLockMgr.mu.Unlock()
		s.conn.mu.Lock()
		delete(s.conn.held, name)
		s.conn.mu.Unlock()
		return &fakeRows{val: int64(1)}, nil
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

	// 第二个 TryLock 应该失败（不同连接）
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
