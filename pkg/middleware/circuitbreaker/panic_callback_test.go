package circuitbreaker

import (
	"errors"
	"testing"
)

// 用户的 OnStateChange 回调 panic 时，熔断器不能崩溃，状态机仍应正确切换到 Open。
func TestOnStateChangePanic_Recovered(t *testing.T) {
	cb := NewCircuitBreaker(Config{
		Name:        "t",
		ReadyToTrip: func(c Counts) bool { return c.ConsecutiveFailures >= 1 },
		OnStateChange: func(_ string, _ State, _ State) {
			panic("boom from user callback")
		},
	})

	// 触发一次失败 → 应切到 Open，且回调 panic 被 recover，不向上抛出。
	_ = cb.Call(func() error { return errors.New("fail") })

	if got := cb.State(); got != StateOpen {
		t.Fatalf("want StateOpen after trip, got %v", got)
	}
}
