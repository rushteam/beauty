// Package worker 提供可组合到 beauty.App 的后台工作者 Service 实现。
package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/dlock"
)

// TickerOption 配置 Ticker 行为。
type TickerOption func(*tickerConfig)

type tickerConfig struct {
	immediate bool // 启动时立即执行一次
}

// WithImmediate 启动时立即执行一次 fn，不等第一个 interval。
func WithImmediate() TickerOption {
	return func(c *tickerConfig) { c.immediate = true }
}

// tickerService 实现 beauty.Service，按固定间隔执行 fn。
type tickerService struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context)
	cfg      tickerConfig
}

// NewTicker 创建定时执行 fn 的 Service。ctx 取消时停止。
//
//	beauty.New(
//	    worker.NewTicker("cleanup", 5*time.Minute, cleanupFn),
//	)
func NewTicker(name string, interval time.Duration, fn func(ctx context.Context), opts ...TickerOption) *tickerService {
	s := &tickerService{name: name, interval: interval, fn: fn}
	for _, o := range opts {
		o(&s.cfg)
	}
	return s
}

func (s *tickerService) Start(ctx context.Context) error {
	if s.cfg.immediate {
		s.fn(ctx)
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.fn(ctx)
		}
	}
}

func (s *tickerService) String() string {
	return fmt.Sprintf("ticker(%s/%s)", s.name, s.interval)
}

// leaderTickerService 结合 Elector 实现"只有 leader 执行"的 Ticker。
// 只有当选 leader 期间才执行定时任务，失去 leader 身份时暂停。
type leaderTickerService struct {
	name     string
	elector  dlock.Elector
	interval time.Duration
	fn       func(ctx context.Context)
	cfg      tickerConfig
}

// NewLeaderTicker 创建带选主的定时 Service。只有 leader 实例运行 fn。
//
//	elector, _ := dlock.NewElectorAuto()
//	beauty.New(
//	    worker.NewLeaderTicker("sync", elector, 10*time.Second, syncFn),
//	)
func NewLeaderTicker(name string, elector dlock.Elector, interval time.Duration, fn func(ctx context.Context), opts ...TickerOption) *leaderTickerService {
	s := &leaderTickerService{name: name, elector: elector, interval: interval, fn: fn}
	for _, o := range opts {
		o(&s.cfg)
	}
	return s
}

func (s *leaderTickerService) Start(ctx context.Context) error {
	return s.elector.Run(ctx, s.name, func(leaderCtx context.Context) {
		if s.cfg.immediate {
			s.fn(leaderCtx)
		}
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-leaderCtx.Done():
				return
			case <-ticker.C:
				s.fn(leaderCtx)
			}
		}
	})
}

func (s *leaderTickerService) String() string {
	return fmt.Sprintf("leader-ticker(%s/%s)", s.name, s.interval)
}
