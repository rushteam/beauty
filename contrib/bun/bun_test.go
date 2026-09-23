package bun_test

import (
	"context"
	"errors"
	"testing"
	"time"

	bbun "github.com/rushteam/beauty/contrib/bun"
	sqldb "github.com/rushteam/beauty/contrib/sqldb"
	"github.com/uptrace/bun"
	_ "modernc.org/sqlite"
)

type item struct {
	bun.BaseModel `bun:"table:items"`
	ID            int64 `bun:",pk,autoincrement"`
	Name          string
}

func openMem(t *testing.T) *bbun.DB {
	t.Helper()
	db, err := bbun.Open(bbun.Config{
		Driver: "sqlite", DSN: "file::memory:?cache=shared", MaxOpenConns: 1,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Write().NewCreateTable().Model((*item)(nil)).Exec(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOpen_CRUD(t *testing.T) {
	db := openMem(t)
	defer db.Close()

	ctx := context.Background()
	if _, err := db.Write().NewInsert().Model(&item{Name: "a"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var got item
	if err := db.Read().NewSelect().Model(&got).Where("name = ?", "a").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if got.Name != "a" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestResilientWrite_CircuitOpens(t *testing.T) {
	db := openMem(t)
	defer db.Close()

	rw := db.ResilientWrite(sqldb.PathResilience{
		Breaker: &sqldb.BreakerSettings{
			Threshold: 0.5, MinRequests: 2, Window: time.Minute, Cooldown: time.Minute,
		},
		Pool:      db.Primary(),
		IsFailure: func(err error) bool { return err != nil },
	})

	ctx := context.Background()
	failSQL := "UPDATE __bad SET name = ? WHERE id = ?"
	for i := 0; i < 8; i++ {
		_, _ = rw.ExecContext(ctx, failSQL, "x", 1)
	}
	start := time.Now()
	_, err := rw.ExecContext(ctx, failSQL, "x", 1)
	if err == nil {
		t.Fatal("expected error")
	}
	// 熔断后 Hook 通过 cancel cause 快速失败,可能表现为 context.Canceled 或 ErrCircuitOpen。
	if !errors.Is(err, sqldb.ErrCircuitOpen) && !errors.Is(err, context.Canceled) {
		t.Fatalf("want circuit open or canceled, got %v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("circuit should fail fast, took %v", time.Since(start))
	}
}

func TestResilientWrite_Insert(t *testing.T) {
	db := openMem(t)
	defer db.Close()

	rw := db.ResilientWrite(sqldb.DefaultWriteResilience(db.Primary()))
	ctx := context.Background()
	if _, err := rw.NewInsert().Model(&item{Name: "b"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}
