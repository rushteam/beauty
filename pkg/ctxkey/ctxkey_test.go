package ctxkey_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/pkg/ctxkey"
)

type user struct{ ID string }

func TestKey_GetMissing(t *testing.T) {
	k := ctxkey.New[user]()
	_, ok := ctxkey.Get(context.Background(), k)
	if ok {
		t.Fatal("missing key should return ok=false")
	}
}

func TestKey_RoundTrip(t *testing.T) {
	k := ctxkey.New[user]()
	ctx := ctxkey.With(context.Background(), k, user{ID: "u1"})
	v, ok := ctxkey.Get(ctx, k)
	if !ok || v.ID != "u1" {
		t.Fatalf("want u1, got %+v ok=%v", v, ok)
	}
}

func TestKey_SameTypeDifferentKeys_Isolated(t *testing.T) {
	k1 := ctxkey.New[string]()
	k2 := ctxkey.New[string]()
	ctx := ctxkey.With(context.Background(), k1, "a")
	ctx = ctxkey.With(ctx, k2, "b")
	v1, _ := ctxkey.Get(ctx, k1)
	v2, _ := ctxkey.Get(ctx, k2)
	if v1 != "a" || v2 != "b" {
		t.Fatalf("isolated keys want a/b, got %q/%q", v1, v2)
	}
	// k2 不应读到 k1 的值
	ctx1 := ctxkey.With(context.Background(), k1, "a")
	if _, ok := ctxkey.Get(ctx1, k2); ok {
		t.Fatal("k2 should not read k1's value")
	}
}

func TestKey_DifferentTypes(t *testing.T) {
	ks := ctxkey.New[string]()
	ki := ctxkey.New[int]()
	ctx := ctxkey.With(context.Background(), ks, "x")
	ctx = ctxkey.With(ctx, ki, 42)
	s, _ := ctxkey.Get(ctx, ks)
	i, _ := ctxkey.Get(ctx, ki)
	if s != "x" || i != 42 {
		t.Fatalf("want x/42, got %q/%d", s, i)
	}
}

func TestMustGet_ReturnsZero(t *testing.T) {
	k := ctxkey.New[user]()
	v := ctxkey.MustGet(context.Background(), k)
	if v.ID != "" {
		t.Fatalf("zero value want empty ID, got %q", v.ID)
	}
}

func TestMustGet_ReturnsValue(t *testing.T) {
	k := ctxkey.New[user]()
	ctx := ctxkey.With(context.Background(), k, user{ID: "u1"})
	if ctxkey.MustGet(ctx, k).ID != "u1" {
		t.Fatal("MustGet should return value")
	}
}

func TestKey_ZeroValueNoPanic(t *testing.T) {
	var k ctxkey.Key[string] // 零值
	ctx := context.Background()
	if _, ok := ctxkey.Get(ctx, k); ok {
		t.Fatal("zero key should not match")
	}
	// 零值 With 也能用(虽然不推荐)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero key With panicked: %v", r)
		}
	}()
	ctx2 := ctxkey.With(ctx, k, "v")
	v, _ := ctxkey.Get(ctx2, k)
	if v != "v" {
		t.Fatalf("zero key round-trip want v, got %q", v)
	}
}

func TestKey_StableAcrossCalls(t *testing.T) {
	// 同一包级变量多次存取应稳定。
	k := ctxkey.New[string]()
	ctx := context.Background()
	for range 100 {
		ctx = ctxkey.With(ctx, k, "v")
	}
	v, _ := ctxkey.Get(ctx, k)
	if v != "v" {
		t.Fatal("repeated With should keep value stable")
	}
}
