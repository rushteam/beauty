package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// GCRA(Generic Cell Rate Algorithm)限流器:漏桶的一种精巧实现——每个 key 只维护一个
// "理论到达时间"(TAT),放行判断只是一次时间算术,无后台补令牌、无请求时间戳队列,
// 输出天然平滑且支持突发。相较令牌桶/滑动窗口,内存最省(每 key 一个时间戳)、无锯齿。
//
// 参数:rate 为每秒允许的稳定速率(emission = 1s/rate 为两次放行的最小间隔);
// burst 为可一次性突发的数量(容忍度 τ = emission*burst)。rate<=0 或 burst<=0 视为不限。
//
// 实现 Limiter,可直接接 Middleware。并发安全;Stop 后 gc 退出。
type GCRA struct {
	emission   time.Duration // 每个"令牌"的时间间隔 = 1s/rate
	tolerance  time.Duration // 突发容忍度 = emission*burst
	maxIdle    time.Duration
	gcInterval time.Duration
	mu         sync.Mutex
	tats       map[string]time.Time // 每 key 的理论到达时间(TAT)
	stop       chan struct{}
	once       sync.Once
}

// NewGCRA 创建 GCRA 限流器。rate=每秒稳定速率,burst=可突发数(>=1)。
func NewGCRA(rate float64, burst int, opts ...Option) *GCRA {
	cfg := config{maxIdle: 5 * time.Minute, gcInterval: time.Minute}
	for _, o := range opts {
		o(&cfg)
	}
	g := &GCRA{
		maxIdle:    cfg.maxIdle,
		gcInterval: cfg.gcInterval,
		tats:       make(map[string]time.Time),
		stop:       make(chan struct{}),
	}
	if rate > 0 && burst > 0 {
		g.emission = time.Duration(float64(time.Second) / rate)
		g.tolerance = time.Duration(burst) * g.emission
	}
	go g.gc()
	return g
}

// Allow 按 key 限流(每次消耗 1)。返回是否放行,以及超限时建议的重试等待。
func (g *GCRA) Allow(key string) (bool, time.Duration) {
	if g.emission <= 0 { // 不限
		return true, 0
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()

	tat := g.tats[key]
	if tat.Before(now) { // 空闲已久:TAT 追平到当前
		tat = now
	}
	newTAT := tat.Add(g.emission)
	// 允许的最早时刻 = newTAT - 容忍度。now 早于它则拒绝。
	allowAt := newTAT.Add(-g.tolerance)
	if now.Before(allowAt) {
		return false, allowAt.Sub(now)
	}
	g.tats[key] = newTAT
	return true, 0
}

// gc 周期清理长时间未活动的 key(TAT 已远早于当前)。
func (g *GCRA) gc() {
	ticker := time.NewTicker(g.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-g.maxIdle)
			g.mu.Lock()
			for k, tat := range g.tats {
				if tat.Before(cutoff) {
					delete(g.tats, k)
				}
			}
			g.mu.Unlock()
		case <-g.stop:
			return
		}
	}
}

// Stop 停止 gc goroutine。幂等。
func (g *GCRA) Stop() {
	g.once.Do(func() { close(g.stop) })
}

// String 便于日志/调试。
func (g *GCRA) String() string {
	if g.emission <= 0 {
		return "GCRA(unlimited)"
	}
	return fmt.Sprintf("GCRA(emission=%s, tolerance=%s)", g.emission, g.tolerance)
}
