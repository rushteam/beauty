package loadbalance

import (
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// ===== P2C + EWMA =====
//
// P2C(Power of Two Choices)+ EWMA:随机取两个节点、选"负载"更低的那个;负载由每节点的
// 延迟 EWMA(指数加权移动平均,带时间衰减)与在途请求数共同决定。相比轮询/加权,它能感知
// 实时快慢与堆积,自动避开慢/过载节点,显著压低长尾——是 gRPC/Finagle/Kratos 的现代默认策略。
//
// 与本包其它算法不同,P2C 需要"结果反馈":Pick 返回节点与一个 done 回调,调用方在请求结束时
// 调用 done(err),用本次耗时与成败更新该节点的 EWMA。用法:
//
//	node, done, ok := lb.Pick()
//	if !ok { /* 无可用节点 */ }
//	err := call(node)
//	done(err)
//
// 算法数学参考自 beauty 的 gRPC balancer(pkg/client/grpcclient,策略名 p2c_ewma):
// load = sqrt(lag+1) * (inflight+1);EWMA 权重 w = exp(-Δt/decay);另有 forcePick 防饥饿
// (长时间未被选中的节点会被强制选一次)与健康度阈值。并发安全。零值不可用,用 NewP2C 构造。
type P2C[T any] struct {
	mu    sync.Mutex
	conns []*p2cConn[T]
	start time.Time
}

const (
	p2cForcePick       = int64(time.Second)      // 节点超过此时长未被选中则强制选一次(防饥饿)
	p2cInitSuccess     = uint64(1000)            // 成功度初值/满值
	p2cThrottleSuccess = p2cInitSuccess / 2      // 低于此视为不健康
	p2cDecayTime       = int64(time.Second * 10) // EWMA 衰减时间常数
)

type p2cConn[T any] struct {
	node     T
	id       string
	lag      uint64 // 延迟 EWMA(纳秒)
	inflight int64  // 在途请求数
	success  uint64 // 成功度 EWMA(健康信号)
	last     int64  // 上次 done 的时间点(相对 start 的纳秒)
	pick     int64  // 上次被选中的时间点
}

// load 负载估计:sqrt(lag) 抑制抖动,乘 (inflight+1) 反映堆积。空闲连接给极大值以便优先探测。
func (c *p2cConn[T]) load() int64 {
	lag := int64(math.Sqrt(float64(atomic.LoadUint64(&c.lag) + 1)))
	load := lag * (atomic.LoadInt64(&c.inflight) + 1)
	if load == 0 {
		return 1<<31 - 1
	}
	return load
}

func (c *p2cConn[T]) healthy() bool {
	return atomic.LoadUint64(&c.success) > p2cThrottleSuccess
}

// NewP2C 创建 P2C+EWMA 均衡器。nodes 为空时 Pick 返回零值 + false。
func NewP2C[T any](nodes []T) *P2C[T] {
	p := &P2C[T]{start: time.Now()}
	p.Update(nodes)
	return p
}

func (p *P2C[T]) nowNs() int64 { return int64(time.Since(p.start)) }

// Update 用新节点列表重建。已存在的节点(按 ID)保留其 EWMA 统计,新增节点初始化。
func (p *P2C[T]) Update(nodes []T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	old := make(map[string]*p2cConn[T], len(p.conns))
	for _, c := range p.conns {
		old[c.id] = c
	}
	next := make([]*p2cConn[T], 0, len(nodes))
	for _, n := range nodes {
		nd, ok := any(n).(Node[T])
		if !ok {
			continue
		}
		id := nd.ID()
		if c, ok := old[id]; ok {
			c.node = n // 保留统计,刷新节点值
			next = append(next, c)
			continue
		}
		next = append(next, &p2cConn[T]{node: n, id: id, success: p2cInitSuccess})
	}
	p.conns = next
}

// Pick 选择一个节点并返回上报回调 done。无可用节点时返回零值 + nil + false。
// 必须在请求结束时调用 done(err),否则该节点的在途计数不归零、EWMA 不更新。
func (p *P2C[T]) Pick() (node T, done func(err error), ok bool) {
	p.mu.Lock()
	var chosen *p2cConn[T]
	switch len(p.conns) {
	case 0:
		p.mu.Unlock()
		var zero T
		return zero, nil, false
	case 1:
		chosen = p.choose(p.conns[0], nil)
	case 2:
		chosen = p.choose(p.conns[0], p.conns[1])
	default:
		var a, b *p2cConn[T]
		for i := 0; i < 3; i++ { // 最多试 3 次,尽量取到两个健康节点
			x := rand.IntN(len(p.conns))
			y := rand.IntN(len(p.conns) - 1)
			if y >= x { // 保证 x != y 且分布均匀
				y++
			}
			a, b = p.conns[x], p.conns[y]
			if a.healthy() && b.healthy() {
				break
			}
		}
		chosen = p.choose(a, b)
	}
	p.mu.Unlock()

	atomic.AddInt64(&chosen.inflight, 1)
	start := p.nowNs()
	return chosen.node, p.buildDone(chosen, start), true
}

// choose 在两节点间选负载低者;forcePick 保证长时间未选中的节点也能被探测(防饥饿)。
// 调用时持有 p.mu。
func (p *P2C[T]) choose(c1, c2 *p2cConn[T]) *p2cConn[T] {
	now := p.nowNs()
	if c2 == nil {
		c1.pick = now
		return c1
	}
	if c1.load() > c2.load() {
		c1, c2 = c2, c1 // 保证 c1 是低负载者
	}
	// 高负载者 c2 若已很久没被选中,强制选它一次做探测,避免它因偶发慢而永久饥饿。
	if now-c2.pick > p2cForcePick {
		c2.pick = now
		return c2
	}
	c1.pick = now
	return c1
}

// buildDone 生成结果上报回调:更新在途数、延迟 EWMA 与成功度 EWMA。
func (p *P2C[T]) buildDone(c *p2cConn[T], start int64) func(err error) {
	return func(err error) {
		atomic.AddInt64(&c.inflight, -1)
		now := p.nowNs()
		last := atomic.SwapInt64(&c.last, now)
		td := now - last
		if td < 0 {
			td = 0
		}
		w := math.Exp(float64(-td) / float64(p2cDecayTime)) // 距上次越久,旧值权重越低
		lag := now - start
		if lag < 0 {
			lag = 0
		}
		olag := atomic.LoadUint64(&c.lag)
		if olag == 0 { // 首次采样:直接用本次值,不与 0 混合
			w = 0
		}
		atomic.StoreUint64(&c.lag, uint64(float64(olag)*w+float64(lag)*(1-w)))

		success := p2cInitSuccess
		if err != nil {
			success = 0
		}
		osucc := atomic.LoadUint64(&c.success)
		atomic.StoreUint64(&c.success, uint64(float64(osucc)*w+float64(success)*(1-w)))
	}
}

// Nodes 返回当前所有节点。
func (p *P2C[T]) Nodes() []T {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]T, len(p.conns))
	for i, c := range p.conns {
		out[i] = c.node
	}
	return out
}
