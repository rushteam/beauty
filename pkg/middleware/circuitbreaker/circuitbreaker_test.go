package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_BasicFunctionality(t *testing.T) {
	config := Config{
		Name:        "test",
		MaxRequests: 3,
		Interval:    time.Minute,
		Timeout:     time.Minute,
		ReadyToTrip: func(counts Counts) bool {
			return counts.Requests >= 3 && counts.TotalFailures >= 2
		},
	}

	cb := NewCircuitBreaker(config)

	// 初始状态应该是关闭的
	if cb.State() != StateClosed {
		t.Errorf("Expected initial state to be Closed, got %v", cb.State())
	}

	// 测试成功请求
	err := cb.Call(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// 测试失败请求
	testError := errors.New("test error")
	err = cb.Call(func() error {
		return testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}

	// 再次失败，应该触发熔断
	err = cb.Call(func() error {
		return testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}

	// 第三次失败，应该触发熔断
	err = cb.Call(func() error {
		return testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}

	// 现在熔断器应该是开启状态
	if cb.State() != StateOpen {
		t.Errorf("Expected state to be Open, got %v", cb.State())
	}

	// 下一个请求应该被熔断器拒绝
	err = cb.Call(func() error {
		return nil
	})
	if err != ErrCircuitBreakerOpen {
		t.Errorf("Expected ErrCircuitBreakerOpen, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpenState(t *testing.T) {
	config := Config{
		Name:        "test-half-open",
		MaxRequests: 2,
		Interval:    time.Minute,
		Timeout:     100 * time.Millisecond, // 短超时时间用于测试
		ReadyToTrip: func(counts Counts) bool {
			return counts.TotalFailures >= 1
		},
	}

	cb := NewCircuitBreaker(config)

	// 触发熔断
	cb.Call(func() error {
		return errors.New("test error")
	})

	// 等待超时，进入半开状态
	time.Sleep(150 * time.Millisecond)

	// 现在应该是半开状态
	if cb.State() != StateHalfOpen {
		t.Errorf("Expected state to be HalfOpen, got %v", cb.State())
	}

	// 在半开状态下成功请求应该关闭熔断器
	err := cb.Call(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	err = cb.Call(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// 现在应该回到关闭状态
	if cb.State() != StateClosed {
		t.Errorf("Expected state to be Closed, got %v", cb.State())
	}
}

func TestCircuitBreaker_Execute(t *testing.T) {
	config := DefaultConfig("test-execute")
	cb := NewCircuitBreaker(config)

	// 测试成功执行
	result, err := cb.Execute(func() (interface{}, error) {
		return "success", nil
	})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("Expected 'success', got %v", result)
	}

	// 测试失败执行
	testError := errors.New("test error")
	result, err = cb.Execute(func() (interface{}, error) {
		return nil, testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	config := Config{
		Name:        "test-reset",
		MaxRequests: 1,
		Interval:    time.Minute,
		Timeout:     time.Minute,
		ReadyToTrip: func(counts Counts) bool {
			return counts.TotalFailures >= 1
		},
	}

	cb := NewCircuitBreaker(config)

	// 触发熔断
	cb.Call(func() error {
		return errors.New("test error")
	})

	// 验证熔断器是开启状态
	if cb.State() != StateOpen {
		t.Errorf("Expected state to be Open, got %v", cb.State())
	}

	// 重置熔断器
	cb.Reset()

	// 验证熔断器回到关闭状态
	if cb.State() != StateClosed {
		t.Errorf("Expected state to be Closed after reset, got %v", cb.State())
	}

	// 验证计数器被重置
	counts := cb.Counts()
	if counts.Requests != 0 || counts.TotalFailures != 0 || counts.TotalSuccesses != 0 {
		t.Errorf("Expected counts to be reset, got %+v", counts)
	}
}

func TestCircuitBreaker_Counts(t *testing.T) {
	config := DefaultConfig("test-counts")
	cb := NewCircuitBreaker(config)

	// 执行一些请求
	cb.Call(func() error { return nil })                 // 成功
	cb.Call(func() error { return errors.New("error") }) // 失败
	cb.Call(func() error { return nil })                 // 成功

	counts := cb.Counts()
	if counts.Requests != 3 {
		t.Errorf("Expected 3 requests, got %d", counts.Requests)
	}
	if counts.TotalSuccesses != 2 {
		t.Errorf("Expected 2 successes, got %d", counts.TotalSuccesses)
	}
	if counts.TotalFailures != 1 {
		t.Errorf("Expected 1 failure, got %d", counts.TotalFailures)
	}
}
