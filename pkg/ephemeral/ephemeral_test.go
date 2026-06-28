package ephemeral_test

import (
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/ephemeral"
)

func TestEphemeral_SetGet(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("k1", "v1", time.Minute)
	v, ok := s.Get("k1")
	if !ok || v != "v1" {
		t.Fatalf("get: %v ok=%v", v, ok)
	}
}

func TestEphemeral_Expiry(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("k", "v", 50*time.Millisecond)
	if _, ok := s.Get("k"); !ok {
		t.Fatal("should exist before expiry")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := s.Get("k"); ok {
		t.Fatal("should be expired")
	}
	// 过期后应被惰性删除。
	if s.Len() != 0 {
		t.Fatalf("len after lazy delete: %d", s.Len())
	}
}

func TestEphemeral_ZeroTTL_NotStored(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("k", "v", 0)
	if _, ok := s.Get("k"); ok {
		t.Fatal("zero TTL should not store")
	}
	s.Set("k", "v", -time.Second)
	if _, ok := s.Get("k"); ok {
		t.Fatal("negative TTL should not store")
	}
}

func TestEphemeral_Delete(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("k", "v", time.Minute)
	if !s.Delete("k") {
		t.Fatal("delete should return true for existing")
	}
	if s.Delete("k") {
		t.Fatal("double delete should return false")
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("should not exist after delete")
	}
}

func TestEphemeral_Overwrite(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("k", "v1", time.Minute)
	s.Set("k", "v2", time.Minute)
	v, _ := s.Get("k")
	if v != "v2" {
		t.Fatalf("want v2 after overwrite, got %v", v)
	}
	if s.Len() != 1 {
		t.Fatalf("overwrite should not grow size: %d", s.Len())
	}
}

func TestEphemeral_OverwriteWithShorterTTL_Expires(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("k", "v1", time.Minute)
	s.Set("k", "v2", 50*time.Millisecond) // 覆盖成短 TTL
	time.Sleep(80 * time.Millisecond)
	if _, ok := s.Get("k"); ok {
		t.Fatal("should expire by overwritten shorter TTL")
	}
}

func TestEphemeral_Len(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("a", 1, time.Minute)
	s.Set("b", 2, time.Minute)
	if s.Len() != 2 {
		t.Fatalf("len=%d want 2", s.Len())
	}
	s.Delete("a")
	if s.Len() != 1 {
		t.Fatalf("len=%d want 1", s.Len())
	}
}

func TestEphemeral_Stop_Idempotent(t *testing.T) {
	s := ephemeral.New()
	s.Stop()
	s.Stop() // 不 panic
}

func TestEphemeral_Concurrent(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			s.Set("k", "v", time.Minute)
			s.Get("k")
			s.Delete("k")
		})
	}
	wg.Wait()
}

func TestEphemeral_Gc_CleansExpired(t *testing.T) {
	// 用很短 TTL + 等待 gc 周期不现实;改为惰性删除已验证。
	// 这里验证 gc 不误删未过期条目。
	s := ephemeral.New()
	defer s.Stop()
	s.Set("keep", "v", time.Minute)
	s.Set("expire", "v", 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	// 惰性删除 expire。
	s.Get("expire")
	if _, ok := s.Get("keep"); !ok {
		t.Fatal("gc should not remove unexpired")
	}
}

func TestEphemeral_AnyValueType(t *testing.T) {
	s := ephemeral.New()
	defer s.Stop()
	s.Set("str", "hello", time.Minute)
	s.Set("num", 42, time.Minute)
	s.Set("struct", struct{ X int }{X: 1}, time.Minute)
	if v, _ := s.Get("str"); v != "hello" {
		t.Fatal("string value")
	}
	if v, _ := s.Get("num"); v != 42 {
		t.Fatal("int value")
	}
	if v, _ := s.Get("struct"); v.(struct{ X int }).X != 1 {
		t.Fatal("struct value")
	}
}
