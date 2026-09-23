package sqldb_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/sqldb"
)

type stubDB struct {
	mu       sync.Mutex
	execErr  error
	queryErr error
	delay    time.Duration
	inFlight int32
}

func (s *stubDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	atomic.AddInt32(&s.inFlight, 1)
	defer atomic.AddInt32(&s.inFlight, -1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return stubResult(1), s.execErr
}

func (s *stubDB) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	atomic.AddInt32(&s.inFlight, 1)
	defer atomic.AddInt32(&s.inFlight, -1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return nil, s.queryErr
}

func (s *stubDB) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

type stubResult int64

func (stubResult) LastInsertId() (int64, error)   { return 0, nil }
func (s stubResult) RowsAffected() (int64, error) { return int64(s), nil }

func TestDefaultIsFailure_LockWait(t *testing.T) {
	err := fmt.Errorf("Error 1205 (HY000): Lock wait timeout exceeded; try restarting transaction")
	if !sqldb.DefaultIsFailure(err) {
		t.Fatal("1205 should count as failure")
	}
	if sqldb.DefaultIsFailure(context.Canceled) {
		t.Fatal("context.Canceled should not count as failure")
	}
}

func TestWithResilience_CircuitOpens(t *testing.T) {
	stub := &stubDB{execErr: errors.New("Error 1205 (HY000): Lock wait timeout exceeded")}
	rw := sqldb.WithResilience(stub, sqldb.PathResilience{
		Breaker: &sqldb.BreakerSettings{
			Threshold:   0.5,
			Window:      time.Minute,
			Cooldown:    time.Minute,
			HalfOpenMax: 1,
			MinRequests: 2,
		},
	})

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = rw.ExecContext(ctx, "UPDATE t SET v=1")
	}
	_, err := rw.ExecContext(ctx, "UPDATE t SET v=1")
	if !errors.Is(err, sqldb.ErrCircuitOpen) {
		t.Fatalf("want circuit open, got %v", err)
	}
}

func TestWithResilience_ConcurrencyLimit(t *testing.T) {
	stub := &stubDB{delay: 200 * time.Millisecond}
	rw := sqldb.WithResilience(stub, sqldb.PathResilience{
		MaxConcurrent:     2,
		MaxConcurrentWait: 0,
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rw.ExecContext(ctx, "UPDATE t SET v=1")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var limited int
	for err := range errs {
		if errors.Is(err, sqldb.ErrConcurrencyLimited) {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("expected at least one concurrency limited error")
	}
}

func TestWithResilience_ReadWriteIndependent(t *testing.T) {
	writeStub := &stubDB{execErr: errors.New("Error 1205: Lock wait timeout exceeded")}
	readStub := &stubDB{}

	write := sqldb.WithResilience(writeStub, sqldb.PathResilience{
		Breaker: &sqldb.BreakerSettings{Threshold: 0.3, MinRequests: 1, Window: time.Minute, Cooldown: time.Minute},
	})
	read := sqldb.WithResilience(readStub, sqldb.PathResilience{
		Breaker: &sqldb.BreakerSettings{Threshold: 0.3, MinRequests: 1, Window: time.Minute, Cooldown: time.Minute},
	})

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = write.ExecContext(ctx, "UPDATE t SET v=1")
	}
	if _, err := read.ExecContext(ctx, "SELECT 1"); err != nil {
		t.Fatalf("read path should not be tripped by write breaker, got %v", err)
	}
}

func TestResilientWriter_Integration(t *testing.T) {
	db, err := sqldb.Open(sqldb.Config{
		Driver: "sqlite", PrimaryDSN: ":memory:", MaxOpenConns: 2, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	w := db.ResilientWriter(sqldb.DefaultWriteResilience(db.Primary()))
	ctx := context.Background()
	if _, err := w.ExecContext(ctx, "CREATE TABLE t(v INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO t(v) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
}
