package circuitbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State 表示熔断器的状态
type State int32

const (
	// StateClosed 关闭状态 - 正常处理请求
	StateClosed State = iota
	// StateOpen 开启状态 - 拒绝请求
	StateOpen
	// StateHalfOpen 半开状态 - 允许少量请求通过以测试服务是否恢复
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	// ErrCircuitBreakerOpen 熔断器开启时返回的错误
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")
	// ErrTooManyRequests 半开状态下请求过多时返回的错误
	ErrTooManyRequests = errors.New("too many requests")
)

// Config 熔断器配置
type Config struct {
	// Name 熔断器名称，用于监控和日志
	Name string
	// MaxRequests 半开状态下允许的最大请求数
	MaxRequests uint32
	// Interval 统计窗口时间间隔
	Interval time.Duration
	// Timeout 熔断器开启后的超时时间，超时后进入半开状态
	Timeout time.Duration
	// ReadyToTrip 判断是否应该熔断的函数
	ReadyToTrip func(counts Counts) bool
	// OnStateChange 状态变化时的回调函数
	OnStateChange func(name string, from State, to State)
}

// Counts 统计计数器
type Counts struct {
	Requests             uint32 // 总请求数
	TotalSuccesses       uint32 // 总成功数
	TotalFailures        uint32 // 总失败数
	ConsecutiveSuccesses uint32 // 连续成功数
	ConsecutiveFailures  uint32 // 连续失败数
}

// CircuitBreaker 熔断器
type CircuitBreaker struct {
	name          string
	maxRequests   uint32
	interval      time.Duration
	timeout       time.Duration
	readyToTrip   func(counts Counts) bool
	onStateChange func(name string, from State, to State)

	mutex      sync.Mutex
	state      State
	generation uint64
	counts     Counts
	expiry     time.Time
}

// NewCircuitBreaker 创建新的熔断器
func NewCircuitBreaker(config Config) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:          config.Name,
		maxRequests:   config.MaxRequests,
		interval:      config.Interval,
		timeout:       config.Timeout,
		readyToTrip:   config.ReadyToTrip,
		onStateChange: config.OnStateChange,
	}

	if cb.name == "" {
		cb.name = "CircuitBreaker"
	}
	if cb.maxRequests == 0 {
		cb.maxRequests = 1
	}
	if cb.interval <= 0 {
		cb.interval = time.Minute
	}
	if cb.timeout <= 0 {
		cb.timeout = time.Minute
	}
	if cb.readyToTrip == nil {
		cb.readyToTrip = defaultReadyToTrip
	}

	cb.toNewGeneration(time.Now())
	return cb
}

// defaultReadyToTrip 默认的熔断判断逻辑
func defaultReadyToTrip(counts Counts) bool {
	return counts.Requests >= 20 && counts.TotalFailures > counts.TotalSuccesses
}

// Name 返回熔断器名称
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// State 返回当前状态
func (cb *CircuitBreaker) State() State {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, _ := cb.currentState(now)
	return state
}

// Counts 返回当前统计信息
func (cb *CircuitBreaker) Counts() Counts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return cb.counts
}

// Execute 执行函数，如果熔断器允许的话
func (cb *CircuitBreaker) Execute(req func() (interface{}, error)) (interface{}, error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		return nil, err
	}

	defer func() {
		if r := recover(); r != nil {
			cb.afterRequest(generation, false)
			panic(r)
		}
	}()

	result, err := req()
	cb.afterRequest(generation, err == nil)
	return result, err
}

// Call 执行函数，返回是否成功
func (cb *CircuitBreaker) Call(req func() error) error {
	_, err := cb.Execute(func() (interface{}, error) {
		return nil, req()
	})
	return err
}

// beforeRequest 请求前检查
func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	if state == StateOpen {
		return generation, ErrCircuitBreakerOpen
	} else if state == StateHalfOpen && cb.counts.Requests >= cb.maxRequests {
		return generation, ErrTooManyRequests
	}

	cb.counts.Requests++
	return generation, nil
}

// afterRequest 请求后处理
func (cb *CircuitBreaker) afterRequest(before uint64, success bool) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(state, now)
	} else {
		cb.onFailure(state, now)
	}
}

// onSuccess 成功时的处理
func (cb *CircuitBreaker) onSuccess(state State, now time.Time) {
	switch state {
	case StateClosed:
		cb.counts.TotalSuccesses++
		cb.counts.ConsecutiveSuccesses++
		cb.counts.ConsecutiveFailures = 0
	case StateHalfOpen:
		cb.counts.TotalSuccesses++
		cb.counts.ConsecutiveSuccesses++
		cb.counts.ConsecutiveFailures = 0
		if cb.counts.ConsecutiveSuccesses >= cb.maxRequests {
			cb.setState(StateClosed, now)
		}
	}
}

// onFailure 失败时的处理
func (cb *CircuitBreaker) onFailure(state State, now time.Time) {
	switch state {
	case StateClosed:
		cb.counts.TotalFailures++
		cb.counts.ConsecutiveFailures++
		cb.counts.ConsecutiveSuccesses = 0
		if cb.readyToTrip(cb.counts) {
			cb.setState(StateOpen, now)
		}
	case StateHalfOpen:
		cb.setState(StateOpen, now)
	}
}

// currentState 获取当前状态
func (cb *CircuitBreaker) currentState(now time.Time) (State, uint64) {
	switch cb.state {
	case StateClosed:
		if !cb.expiry.IsZero() && cb.expiry.Before(now) {
			cb.toNewGeneration(now)
		}
	case StateOpen:
		if cb.expiry.Before(now) {
			cb.setState(StateHalfOpen, now)
		}
	}
	return cb.state, cb.generation
}

// setState 设置状态
func (cb *CircuitBreaker) setState(state State, now time.Time) {
	if cb.state == state {
		return
	}

	prev := cb.state
	cb.state = state

	cb.toNewGeneration(now)

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

// toNewGeneration 开始新的统计周期
func (cb *CircuitBreaker) toNewGeneration(now time.Time) {
	cb.generation++
	cb.counts = Counts{}

	var zero time.Time
	switch cb.state {
	case StateClosed:
		if cb.interval == 0 {
			cb.expiry = zero
		} else {
			cb.expiry = now.Add(cb.interval)
		}
	case StateOpen:
		cb.expiry = now.Add(cb.timeout)
	default: // StateHalfOpen
		cb.expiry = zero
	}
}

// Reset 重置熔断器到关闭状态
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.toNewGeneration(time.Now())
	cb.setState(StateClosed, time.Now())
}

// String 返回熔断器的字符串表示
func (cb *CircuitBreaker) String() string {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return fmt.Sprintf("CircuitBreaker[%s: %s, %+v]", cb.name, cb.state.String(), cb.counts)
}
