package ratelimit

import (
	"context"
	"testing"
	"time"
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
	metadata := map[string]interface{}{"default": "test"}

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
	metadata := map[string]interface{}{"default": "test"}

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
	metadata := map[string]interface{}{
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
	metadata = map[string]interface{}{
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

func TestUserKeyExtractor(t *testing.T) {
	extractor := NewUserKeyExtractor("user_id")

	// 测试从元数据中提取用户ID
	metadata := map[string]interface{}{
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
