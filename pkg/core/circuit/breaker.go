package circuit

import (
	"fmt"
	"sync"
	"time"
)

var ErrCircuitOpen = fmt.Errorf("circuit ooen error")

type Breaker struct {
	mutex     sync.RWMutex
	failures  int64
	lastFail  time.Time
	threshold int64
	timeout   time.Duration
	isOpen    bool
}

func NewBreaker(threshold int64, timeout time.Duration) *Breaker {
	return &Breaker{
		threshold: threshold,
		timeout:   timeout,
	}
}

func (b *Breaker) Execute(fn func() error) error {
	if !b.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()
	b.recordResult(err)
	return err
}
func (b *Breaker) allowRequest() bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	if !b.isOpen {
		return true
	}

	if time.Since(b.lastFail) > b.timeout {
		b.mutex.RUnlock()
		b.mutex.Lock()
		b.isOpen = false
		b.failures = 0
		b.mutex.Unlock()
		b.mutex.RLock()
		return true
	}

	return false
}

func (b *Breaker) recordResult(err error) {
	if err == nil {
		return
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.failures++
	b.lastFail = time.Now()

	if b.failures >= b.threshold {
		b.isOpen = true
	}
}
