package timeout

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimeoutController_Execute(t *testing.T) {
	config := DefaultConfig("test", 100*time.Millisecond)
	tc := NewTimeoutController(config)

	// 测试成功执行
	err := tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond) // 少于超时时间
		return nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// 测试超时
	err = tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(200 * time.Millisecond) // 超过超时时间
		return nil
	})
	if err != ErrTimeout {
		t.Errorf("Expected ErrTimeout, got %v", err)
	}

	// 测试函数返回错误
	testError := errors.New("test error")
	err = tc.Execute(context.Background(), func(ctx context.Context) error {
		return testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}
}

func TestTimeoutController_ExecuteWithResult(t *testing.T) {
	config := DefaultConfig("test-result", 100*time.Millisecond)
	tc := NewTimeoutController(config)

	// 测试成功执行并返回结果
	result, err := tc.ExecuteWithResult(context.Background(), func(ctx context.Context) (interface{}, error) {
		return "success", nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("Expected 'success', got %v", result)
	}

	// 测试超时
	result, err = tc.ExecuteWithResult(context.Background(), func(ctx context.Context) (interface{}, error) {
		time.Sleep(200 * time.Millisecond)
		return "timeout", nil
	})
	if err != ErrTimeout {
		t.Errorf("Expected ErrTimeout, got %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
}

func TestTimeoutController_Stats(t *testing.T) {
	config := DefaultConfig("test-stats", 100*time.Millisecond)
	tc := NewTimeoutController(config)

	// 执行一些请求
	tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(30 * time.Millisecond) // 快速请求
		return nil
	})

	tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(70 * time.Millisecond) // 慢请求
		return nil
	})

	tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(150 * time.Millisecond) // 超时请求
		return nil
	})

	stats := tc.Stats()
	if stats.TotalRequests != 3 {
		t.Errorf("Expected 3 total requests, got %d", stats.TotalRequests)
	}
	if stats.TimeoutRequests != 1 {
		t.Errorf("Expected 1 timeout request, got %d", stats.TimeoutRequests)
	}
	if stats.SlowRequests != 2 { // 慢请求阈值是50ms
		t.Errorf("Expected 2 slow requests, got %d", stats.SlowRequests)
	}
}

func TestTimeoutController_ResetStats(t *testing.T) {
	config := DefaultConfig("test-reset", 100*time.Millisecond)
	tc := NewTimeoutController(config)

	// 执行一个请求
	tc.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	// 验证统计信息
	stats := tc.Stats()
	if stats.TotalRequests != 1 {
		t.Errorf("Expected 1 total request, got %d", stats.TotalRequests)
	}

	// 重置统计信息
	tc.ResetStats()

	// 验证统计信息被重置
	stats = tc.Stats()
	if stats.TotalRequests != 0 {
		t.Errorf("Expected 0 total requests after reset, got %d", stats.TotalRequests)
	}
}

func TestTimeoutController_Rates(t *testing.T) {
	config := DefaultConfig("test-rates", 100*time.Millisecond)
	tc := NewTimeoutController(config)

	// 执行请求：2个成功，1个超时，1个慢请求
	tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(20 * time.Millisecond) // 快速请求
		return nil
	})

	tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(70 * time.Millisecond) // 慢请求
		return nil
	})

	tc.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(150 * time.Millisecond) // 超时请求
		return nil
	})

	timeoutRate := tc.TimeoutRate()
	expectedTimeoutRate := 1.0 / 3.0 // 1个超时，总共3个请求
	if abs(timeoutRate-expectedTimeoutRate) > 0.01 {
		t.Errorf("Expected timeout rate %.2f, got %.2f", expectedTimeoutRate, timeoutRate)
	}

	slowRate := tc.SlowRate()
	expectedSlowRate := 2.0 / 3.0 // 2个慢请求，总共3个请求
	if abs(slowRate-expectedSlowRate) > 0.01 {
		t.Errorf("Expected slow rate %.2f, got %.2f", expectedSlowRate, slowRate)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestTimeoutController_ContextCancellation(t *testing.T) {
	config := DefaultConfig("test-cancel", 200*time.Millisecond)
	tc := NewTimeoutController(config)

	// 创建一个会被取消的上下文
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel() // 50ms后取消上下文
	}()

	err := tc.Execute(ctx, func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond) // 会被取消
		return nil
	})

	if err != ErrTimeoutCanceled {
		t.Errorf("Expected ErrTimeoutCanceled, got %v", err)
	}
}
