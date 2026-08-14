package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/tenant"
)

func TestRateLimitMiddleware_Allow(t *testing.T) {
	config := Config{
		Name:          "test",
		Rate:          2.0, // 每秒2个请求
		Burst:         2,
		EnableMetrics: true,
		DefaultKey:    "test",
	}

	rl := NewRateLimitMiddleware(config)
	metadata := map[string]any{"default": "test"}

	// 前两个请求应该通过
	err := rl.Allow(context.Background(), metadata)
	if err != nil {
		t.Errorf("Expected no error for first request, got %v", err)
	}

	err = rl.Allow(context.Background(), metadata)
	if err != nil {
		t.Errorf("Expected no error for second request, got %v", err)
	}

	// 第三个请求应该被限流
	err = rl.Allow(context.Background(), metadata)
	if err != ErrRateLimitExceeded {
		t.Errorf("Expected ErrRateLimitExceeded for third request, got %v", err)
	}

	// 检查统计信息
	stats := rl.Stats()
	if stats.TotalRequests != 3 {
		t.Errorf("Expected 3 total requests, got %d", stats.TotalRequests)
	}
	if stats.AllowedRequests != 2 {
		t.Errorf("Expected 2 allowed requests, got %d", stats.AllowedRequests)
	}
	if stats.LimitedRequests != 1 {
		t.Errorf("Expected 1 limited request, got %d", stats.LimitedRequests)
	}
}

func TestRateLimitMiddleware_Wait(t *testing.T) {
	config := Config{
		Name:          "test-wait",
		Rate:          10.0, // 每秒10个请求
		Burst:         1,
		EnableMetrics: true,
		DefaultKey:    "test",
	}

	rl := NewRateLimitMiddleware(config)
	metadata := map[string]any{"default": "test"}

	// 第一个请求应该立即通过
	start := time.Now()
	err := rl.Wait(context.Background(), metadata)
	duration := time.Since(start)

	if err != nil {
		t.Errorf("Expected no error for first request, got %v", err)
	}
	if duration > 10*time.Millisecond {
		t.Errorf("Expected first request to be immediate, took %v", duration)
	}

	// 第二个请求应该需要等待
	start = time.Now()
	err = rl.Wait(context.Background(), metadata)
	duration = time.Since(start)

	if err != nil {
		t.Errorf("Expected no error for second request, got %v", err)
	}
	if duration < 50*time.Millisecond {
		t.Errorf("Expected second request to wait, only took %v", duration)
	}
}

func TestRateLimitMiddleware_UpdateRate(t *testing.T) {
	config := Config{
		Name:          "test-update",
		Rate:          1.0,
		Burst:         1,
		EnableMetrics: true,
		DefaultKey:    "test",
	}

	rl := NewRateLimitMiddleware(config)

	// 验证初始配置
	if rl.LimitRate() != 1.0 {
		t.Errorf("Expected initial rate 1.0, got %f", rl.LimitRate())
	}
	if rl.Burst() != 1 {
		t.Errorf("Expected initial burst 1, got %d", rl.Burst())
	}

	// 更新速率
	rl.UpdateRate(10.0, 10)

	// 验证更新后的配置
	if rl.LimitRate() != 10.0 {
		t.Errorf("Expected updated rate 10.0, got %f", rl.LimitRate())
	}
	if rl.Burst() != 10 {
		t.Errorf("Expected updated burst 10, got %d", rl.Burst())
	}
}

func TestIPKeyExtractor(t *testing.T) {
	extractor := NewIPKeyExtractor()

	// 测试从 remote_addr 提取 IP
	metadata := map[string]any{
		"remote_addr": "192.168.1.100:8080",
	}

	key, err := extractor.Extract(context.Background(), metadata)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if key != "ip:192.168.1.100" {
		t.Errorf("Expected 'ip:192.168.1.100', got '%s'", key)
	}

	// 测试从 X-Forwarded-For 提取 IP
	metadata = map[string]any{
		"headers": map[string][]string{
			"X-Forwarded-For": {"10.0.0.1, 192.168.1.1"},
		},
		"remote_addr": "192.168.1.100:8080",
	}

	key, err = extractor.Extract(context.Background(), metadata)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if key != "ip:10.0.0.1" {
		t.Errorf("Expected 'ip:10.0.0.1', got '%s'", key)
	}
}

func TestRateLimitMiddleware_GC(t *testing.T) {
	rl := NewRateLimitMiddleware(Config{
		Rate:       10,
		Burst:      10,
		IdleTTL:    50 * time.Millisecond,
		GCInterval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rl.StartGC(ctx)

	// 产生两个不同 key 的 limiter
	_ = rl.getEntry("key-a")
	_ = rl.getEntry("key-b")

	if n := len(rl.limiters); n != 2 {
		t.Fatalf("want 2 limiters, got %d", n)
	}

	// 等待 key-a 过期，同时持续访问 key-b 保持活跃
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = rl.getEntry("key-b")
		time.Sleep(5 * time.Millisecond)
	}

	rl.mutex.RLock()
	_, hasA := rl.limiters["key-a"]
	_, hasB := rl.limiters["key-b"]
	rl.mutex.RUnlock()

	if hasA {
		t.Error("key-a should have been GC'd")
	}
	if !hasB {
		t.Error("key-b should still be alive")
	}
}

func TestUserKeyExtractor(t *testing.T) {
	extractor := NewUserKeyExtractor("user_id")

	// 测试从元数据中提取用户ID
	metadata := map[string]any{
		"headers": map[string][]string{
			"X-User-ID": {"123"},
		},
	}

	key, err := extractor.Extract(context.Background(), metadata)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if key != "user:123" {
		t.Errorf("Expected 'user:123', got '%s'", key)
	}
}

func TestTenantKeyExtractor_FromContext(t *testing.T) {
	ext := NewTenantKeyExtractor()
	ctx := tenant.NewContext(context.Background(), "acme")

	key, err := ext.Extract(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "tenant:acme" {
		t.Fatalf("want tenant:acme, got %q", key)
	}
}

func TestTenantKeyExtractor_FallbackHeader(t *testing.T) {
	ext := NewTenantKeyExtractor()
	md := map[string]any{
		"headers": map[string][]string{
			"X-Tenant-ID": {"org2"},
		},
	}
	key, err := ext.Extract(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	if key != "tenant:org2" {
		t.Fatalf("want tenant:org2, got %q", key)
	}
}

func TestTenantKeyExtractor_FallbackMetadataHeader(t *testing.T) {
	ext := NewTenantKeyExtractor()
	md := map[string]any{
		"headers": map[string][]string{
			"x-tenant-id": {"org3"},
		},
	}
	key, err := ext.Extract(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	if key != "tenant:org3" {
		t.Fatalf("want tenant:org3, got %q", key)
	}
}

func TestTenantKeyExtractor_Missing(t *testing.T) {
	ext := NewTenantKeyExtractor()
	_, err := ext.Extract(context.Background(), nil)
	if err == nil {
		t.Fatal("want error for missing tenant")
	}
}

func TestRateOverride_PerKeyQuota(t *testing.T) {
	rl := NewRateLimitMiddleware(Config{
		Rate:  100,
		Burst: 100,
		RateOverride: func(key string) (float64, int, bool) {
			if key == "vip" {
				return 1000, 500, true
			}
			return 0, 0, false
		},
	})

	vipEntry := rl.getEntry("vip")
	if vipEntry.limiter.Limit() != 1000 {
		t.Fatalf("vip rate: want 1000, got %v", vipEntry.limiter.Limit())
	}
	if vipEntry.limiter.Burst() != 500 {
		t.Fatalf("vip burst: want 500, got %d", vipEntry.limiter.Burst())
	}

	defaultEntry := rl.getEntry("normal")
	if defaultEntry.limiter.Limit() != 100 {
		t.Fatalf("default rate: want 100, got %v", defaultEntry.limiter.Limit())
	}
	if defaultEntry.limiter.Burst() != 100 {
		t.Fatalf("default burst: want 100, got %d", defaultEntry.limiter.Burst())
	}
}

func TestRateOverride_NilUsesDefault(t *testing.T) {
	rl := NewRateLimitMiddleware(Config{
		Rate:  50,
		Burst: 25,
	})

	entry := rl.getEntry("any")
	if entry.limiter.Limit() != 50 {
		t.Fatalf("rate: want 50, got %v", entry.limiter.Limit())
	}
	if entry.limiter.Burst() != 25 {
		t.Fatalf("burst: want 25, got %d", entry.limiter.Burst())
	}
}
