package idempotency_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/idempotency"
	"github.com/rushteam/beauty/pkg/kvstore"
)

func TestStore_DedupAndReplay(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	s := idempotency.New[int](idempotency.WithStore(st))
	defer s.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return 42, nil }

	v1, err1, shared1 := s.Do("k", fn)
	if v1 != 42 || err1 != nil || shared1 {
		t.Fatalf("first: (%d,%v,%v)", v1, err1, shared1)
	}
	v2, _, shared2 := s.Do("k", fn)
	if v2 != 42 || !shared2 {
		t.Fatalf("second: (%d,shared=%v), want (42,true)", v2, shared2)
	}
	if calls.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1 (deduped via store)", calls.Load())
	}
}

func TestStore_ErrorNotCached(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	s := idempotency.New[int](idempotency.WithStore(st))
	defer s.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return 0, errors.New("boom") }
	s.Do("k", fn)
	_, _, shared := s.Do("k", fn)
	if shared {
		t.Fatal("error should not be cached in store mode")
	}
	if calls.Load() != 2 {
		t.Fatalf("fn should re-run after error, ran %d", calls.Load())
	}
}

func TestStore_GetAndForget(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	s := idempotency.New[string](idempotency.WithStore(st))
	defer s.Stop()

	if _, ok := s.Get("k"); ok {
		t.Fatal("missing key")
	}
	s.Do("k", func() (string, error) { return "v", nil })
	if v, ok := s.Get("k"); !ok || v != "v" {
		t.Fatalf("get after do: (%q,%v)", v, ok)
	}
	s.Forget("k")
	if _, ok := s.Get("k"); ok {
		t.Fatal("after forget should be gone")
	}
}

// 两个实例共享 store:实例1执行后,实例2 复用结果不重复执行(跨实例去重)。
func TestStore_SharedAcrossInstances(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	s1 := idempotency.New[int](idempotency.WithStore(st))
	defer s1.Stop()
	s2 := idempotency.New[int](idempotency.WithStore(st))
	defer s2.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return 7, nil }

	s1.Do("order:1", fn)                 // 实例1 执行
	v, _, shared := s2.Do("order:1", fn) // 实例2 复用
	if v != 7 || !shared {
		t.Fatalf("cross-instance replay: (%d,shared=%v), want (7,true)", v, shared)
	}
	if calls.Load() != 1 {
		t.Fatalf("fn ran %d times across instances, want 1", calls.Load())
	}
}

// 结构体结果的 JSON 往返。
func TestStore_StructResult(t *testing.T) {
	type reward struct {
		Item string
		Qty  int
	}
	st := kvstore.NewMemory()
	defer st.Stop()
	s := idempotency.New[reward](idempotency.WithStore(st))
	defer s.Stop()

	r1, _, _ := s.Do("draw:1", func() (reward, error) { return reward{"sword", 1}, nil })
	r2, _, shared := s.Do("draw:1", func() (reward, error) { return reward{"axe", 9}, nil })
	if !shared || r2 != r1 || r2.Item != "sword" {
		t.Fatalf("struct replay: r1=%+v r2=%+v shared=%v", r1, r2, shared)
	}
}

func TestStore_FailOpenOnError(t *testing.T) {
	// store 故障时降级为直接执行,不阻断业务。
	var reported atomic.Int64
	s := idempotency.New[int](
		idempotency.WithStore(errStore{}),
		idempotency.WithOnStoreError(func(op, key string, err error) { reported.Add(1) }),
	)
	defer s.Stop()
	v, err, _ := s.Do("k", func() (int, error) { return 5, nil })
	if v != 5 || err != nil {
		t.Fatalf("should fall back to executing fn: (%d,%v)", v, err)
	}
	if reported.Load() == 0 {
		t.Fatal("store error should be reported")
	}
}

// errStore 是恒返回错误的 Store,用于测试 fail-open 降级。
type errStore struct{}

func (errStore) Incr(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errBoom
}
func (errStore) GetInt(context.Context, string) (int64, bool, error)      { return 0, false, errBoom }
func (errStore) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, errBoom }
func (errStore) Set(context.Context, string, []byte, time.Duration) error { return errBoom }
func (errStore) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errBoom
}
func (errStore) TTL(context.Context, string) (time.Duration, bool, error) { return 0, false, errBoom }
func (errStore) Delete(context.Context, string) error                     { return errBoom }

var errBoom = errors.New("store down")
