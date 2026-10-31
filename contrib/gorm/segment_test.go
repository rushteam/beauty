package gorm_test

import (
	"context"
	"testing"

	sqlite "github.com/glebarez/sqlite"

	bgorm "github.com/rushteam/beauty/contrib/gorm"
)

// segmentStore 与 idgen.SegmentStore 同形:编译期确认 *bgorm.SegmentStore 满足该接口。
type segmentStore interface {
	NextRange(ctx context.Context, tag string) (start, count int64, err error)
}

var _ segmentStore = (*bgorm.SegmentStore)(nil)

func openSegDB(t *testing.T) *bgorm.DB {
	t.Helper()
	db, err := bgorm.OpenWith(sqlite.Open(":memory:"), nil, bgorm.Config{MaxOpenConns: 1})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func TestSegmentStoreContiguousRanges(t *testing.T) {
	db := openSegDB(t)
	defer db.Close()
	store := bgorm.NewSegmentStore(db.DB, bgorm.WithDefaultStep(100))
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	// 连续批发的区间必须首尾相接、不重叠。
	var nextExpected int64
	for i := 0; i < 5; i++ {
		start, count, err := store.NextRange(ctx, "order")
		if err != nil {
			t.Fatalf("NextRange: %v", err)
		}
		if count != 100 {
			t.Fatalf("count=%d want 100", count)
		}
		if start != nextExpected {
			t.Fatalf("iter %d start=%d want %d", i, start, nextExpected)
		}
		nextExpected = start + count
	}
}

func TestSegmentStoreMultiTagIsolation(t *testing.T) {
	db := openSegDB(t)
	defer db.Close()
	store := bgorm.NewSegmentStore(db.DB, bgorm.WithDefaultStep(50))
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	a0, _, _ := store.NextRange(ctx, "a")
	b0, _, _ := store.NextRange(ctx, "b")
	a1, _, _ := store.NextRange(ctx, "a")
	if a0 != 0 || b0 != 0 || a1 != 50 {
		t.Fatalf("tag isolation broken: a0=%d b0=%d a1=%d", a0, b0, a1)
	}
}

func TestSegmentStoreNoAutoCreate(t *testing.T) {
	db := openSegDB(t)
	defer db.Close()
	store := bgorm.NewSegmentStore(db.DB, bgorm.WithAutoCreate(false))
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, _, err := store.NextRange(context.Background(), "ghost"); err == nil {
		t.Fatal("expected ErrTagNotFound for unknown tag without auto-create")
	}
}
