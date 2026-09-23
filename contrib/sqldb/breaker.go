// 轻量熔断器(纯标准库),语义对齐 pkg/resilience/circuitbreaker,供 sqldb 独立模块使用。
package sqldb

import (
	"errors"
	"sync"
	"time"
)

var errCircuitOpen = errors.New("sqldb: circuit open")

type cbState int

const (
	cbClosed cbState = iota
	cbOpen
	cbHalfOpen
)

type cbConfig struct {
	threshold   float64
	window      time.Duration
	cooldown    time.Duration
	halfOpenMax int
	minRequests int
}

func defaultCBConfig() cbConfig {
	return cbConfig{
		threshold:   0.5,
		window:      10 * time.Second,
		cooldown:    5 * time.Second,
		halfOpenMax: 3,
		minRequests: 10,
	}
}

type circuitBreaker struct {
	cfg cbConfig

	mu       sync.Mutex
	state    cbState
	epoch    uint64
	openedAt time.Time

	total       int64
	failures    int64
	windowStart time.Time

	halfSucc     int
	halfFail     int
	halfInflight int
}

func newCircuitBreaker(cfg cbConfig) *circuitBreaker {
	if cfg.threshold <= 0 || cfg.threshold > 1 {
		cfg.threshold = 0.5
	}
	if cfg.window <= 0 {
		cfg.window = 10 * time.Second
	}
	if cfg.cooldown <= 0 {
		cfg.cooldown = 5 * time.Second
	}
	if cfg.halfOpenMax <= 0 {
		cfg.halfOpenMax = 3
	}
	if cfg.minRequests <= 0 {
		cfg.minRequests = 10
	}
	return &circuitBreaker{
		cfg:         cfg,
		state:       cbClosed,
		windowStart: time.Now(),
	}
}

// allow 检查当前是否可放行(不计数、不执行 fn)。
func (b *circuitBreaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.checkTransition()
	switch b.state {
	case cbOpen:
		return errCircuitOpen
	case cbHalfOpen:
		if b.halfInflight+b.halfSucc+b.halfFail >= b.cfg.halfOpenMax {
			return errCircuitOpen
		}
	}
	return nil
}

func (b *circuitBreaker) Do(fn func() error) error {
	b.mu.Lock()
	b.checkTransition()

	switch b.state {
	case cbOpen:
		b.mu.Unlock()
		return errCircuitOpen

	case cbHalfOpen:
		if b.halfInflight+b.halfSucc+b.halfFail >= b.cfg.halfOpenMax {
			b.mu.Unlock()
			return errCircuitOpen
		}
		b.halfInflight++
		epoch := b.epoch
		b.mu.Unlock()
		err := fn()
		b.recordHalfOpen(err, epoch)
		return err

	default:
		epoch := b.epoch
		b.mu.Unlock()
		err := fn()
		b.recordClosed(err, epoch)
		return err
	}
}

func (b *circuitBreaker) checkTransition() {
	switch b.state {
	case cbOpen:
		if time.Since(b.openedAt) >= b.cfg.cooldown {
			b.transition(cbHalfOpen)
			b.halfSucc, b.halfFail, b.halfInflight = 0, 0, 0
		}
	case cbClosed:
		if time.Since(b.windowStart) >= b.cfg.window {
			b.resetWindow()
		}
	}
}

func (b *circuitBreaker) recordClosed(err error, epoch uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != cbClosed || b.epoch != epoch {
		return
	}
	if time.Since(b.windowStart) >= b.cfg.window {
		b.resetWindow()
	}

	b.total++
	if err != nil {
		b.failures++
	}
	if b.total >= int64(b.cfg.minRequests) {
		if float64(b.failures)/float64(b.total) >= b.cfg.threshold {
			b.transition(cbOpen)
			b.openedAt = time.Now()
		}
	}
}

func (b *circuitBreaker) recordHalfOpen(err error, epoch uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.halfInflight--
	if b.state != cbHalfOpen || b.epoch != epoch {
		return
	}
	if err != nil {
		b.halfFail++
		b.transition(cbOpen)
		b.openedAt = time.Now()
		return
	}
	b.halfSucc++
	if b.halfSucc >= b.cfg.halfOpenMax {
		b.transition(cbClosed)
		b.resetWindow()
	}
}

func (b *circuitBreaker) transition(to cbState) {
	if b.state == to {
		return
	}
	b.state = to
	b.epoch++
}

func (b *circuitBreaker) resetWindow() {
	b.total, b.failures = 0, 0
	b.windowStart = time.Now()
}
