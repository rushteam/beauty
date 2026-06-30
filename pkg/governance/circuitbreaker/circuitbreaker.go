// Package circuitbreaker 提供节点级熔断器。
//
// 与 pkg/middleware/circuitbreaker 的区别:middleware 版是包请求级(gobreaker 风格,
// 用 Execute 把整个请求函数包进去),适合 HTTP/gRPC 中间件在请求入口统一熔断;
// 本包是节点级,提供 Available(node)+Report(node,err) 分离接口,供客户端 selectService
// 在选实例时跳过已熔断节点——中间隔着负载均衡,无法把整个调用塞进 Execute,故分离设计。
//
// 状态机:Closed(正常)→ 连续失败达阈值 → Open(熔断,拒绝)→ 冷却 timeout 后 →
// HalfOpen(放行 1 个探测)→ 探测成功 → Closed / 探测失败 → Open。
// 每个 node(按 Addr)独立状态,互不影响。并发安全。
//
// 默认配置:连续失败 5 次熔断,Open 冷却 30s,半开放行 1 个探测。可用 Option 覆盖。
package circuitbreaker

import (
	"sync"
	"time"

	"log/slog"

	"github.com/rushteam/beauty/pkg/service/discover"
)

// State 熔断器状态。
type State int

const (
	StateClosed   State = iota // 正常,放行
	StateOpen                  // 熔断,拒绝
	StateHalfOpen              // 半开,放行 1 个探测
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

// CircuitBreaker 节点级熔断器接口。选节点前问 Available,调用后 Report 结果。
type CircuitBreaker interface {
	Available(node *discover.ServiceInfo) bool
	Report(node *discover.ServiceInfo, cost time.Duration, err error)
}

// NoopBreaker 空实现。Available 永真,Report 空操作。未配置熔断时的默认值,零开销。
type NoopBreaker struct{}

func (NoopBreaker) Available(*discover.ServiceInfo) bool               { return true }
func (NoopBreaker) Report(*discover.ServiceInfo, time.Duration, error) {}

// nodeState 单个节点的熔断状态。
type nodeState struct {
	mu                   sync.Mutex
	state                State
	consecutiveFailures  uint32
	consecutiveSuccesses uint32
	openedAt             time.Time // 进入 Open 的时刻
	halfOpenInflight     bool      // 半开态是否已放行探测请求
}

// config 熔断器配置(不导出,通过 Option 设置)。
type config struct {
	failureThreshold uint32        // 连续失败多少次熔断(Closed→Open)
	successThreshold uint32        // 半开态连续成功多少次恢复(HalfOpen→Closed)
	timeout          time.Duration // Open 冷却时间,超时进 HalfOpen
	onStateChange    func(nodeAddr string, from, to State)
}

// Option 配置 NodeBreaker。
type Option func(*config)

// WithFailureThreshold 设置连续失败熔断阈值(默认 5)。
func WithFailureThreshold(n uint32) Option { return func(c *config) { c.failureThreshold = n } }

// WithSuccessThreshold 设置半开态恢复所需的连续成功数(默认 1)。
func WithSuccessThreshold(n uint32) Option { return func(c *config) { c.successThreshold = n } }

// WithTimeout 设置 Open 冷却时间(默认 30s)。
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithOnStateChange 设置节点状态变更回调(日志/metric 用)。回调在锁内执行,须轻量。
func WithOnStateChange(fn func(nodeAddr string, from, to State)) Option {
	return func(c *config) { c.onStateChange = fn }
}

// NodeBreaker 节点级熔断器。按 node.Addr 维护独立状态。
type NodeBreaker struct {
	cfg   config
	mu    sync.RWMutex
	nodes map[string]*nodeState
}

// NewNodeBreaker 创建节点级熔断器。
func NewNodeBreaker(opts ...Option) *NodeBreaker {
	cfg := config{
		failureThreshold: 5,
		successThreshold: 1,
		timeout:          30 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &NodeBreaker{cfg: cfg, nodes: make(map[string]*nodeState)}
}

// getOrCreate 取/建节点的状态。调用方持锁外调用。
func (b *NodeBreaker) getOrCreate(addr string) *nodeState {
	b.mu.RLock()
	ns, ok := b.nodes[addr]
	b.mu.RUnlock()
	if ok {
		return ns
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ns, ok = b.nodes[addr]; ok {
		return ns
	}
	ns = &nodeState{state: StateClosed}
	b.nodes[addr] = ns
	return ns
}

// Available 判断节点是否可用。Open 态返回 false;HalfOpen 态若未放行探测则放行 1 个并返回 true;
// Closed 态返回 true。
func (b *NodeBreaker) Available(node *discover.ServiceInfo) bool {
	if node == nil {
		return false
	}
	ns := b.getOrCreate(node.Addr)
	ns.mu.Lock()
	defer ns.mu.Unlock()

	switch ns.state {
	case StateClosed:
		return true
	case StateOpen:
		// 冷却超时则进 HalfOpen,放行 1 个探测
		if time.Since(ns.openedAt) >= b.cfg.timeout {
			b.setState(ns, node.Addr, StateHalfOpen)
			ns.halfOpenInflight = true
			return true
		}
		return false
	case StateHalfOpen:
		// 半开态只放行 1 个探测请求,其余拒绝
		if !ns.halfOpenInflight {
			ns.halfOpenInflight = true
			return true
		}
		return false
	default:
		return true
	}
}

// Report 反馈一次调用的结果。成功推进 HalfOpen→Closed,失败推进 Closed→Open / HalfOpen→Open。
// cost 暂不参与判断(预留,未来可接自适应阈值),err==nil 视为成功。
func (b *NodeBreaker) Report(node *discover.ServiceInfo, _ time.Duration, err error) {
	if node == nil {
		return
	}
	ns := b.getOrCreate(node.Addr)
	ns.mu.Lock()
	defer ns.mu.Unlock()

	success := err == nil
	switch ns.state {
	case StateClosed:
		if success {
			ns.consecutiveFailures = 0
		} else {
			ns.consecutiveFailures++
			if ns.consecutiveFailures >= b.cfg.failureThreshold {
				b.setState(ns, node.Addr, StateOpen)
			}
		}
	case StateHalfOpen:
		// 探测请求结束,清 inflight 标记
		ns.halfOpenInflight = false
		if success {
			ns.consecutiveSuccesses++
			if ns.consecutiveSuccesses >= b.cfg.successThreshold {
				b.setState(ns, node.Addr, StateClosed)
			}
		} else {
			b.setState(ns, node.Addr, StateOpen)
		}
	case StateOpen:
		// Open 态的 Report 通常是超时放行的探测或并发漏网请求,忽略
	}
}

// setState 切换状态并清计数。调用方持 ns.mu。
func (b *NodeBreaker) setState(ns *nodeState, addr string, to State) {
	if ns.state == to {
		return
	}
	from := ns.state
	ns.state = to
	ns.consecutiveFailures = 0
	ns.consecutiveSuccesses = 0
	ns.halfOpenInflight = false
	if to == StateOpen {
		ns.openedAt = time.Now()
	}
	if b.cfg.onStateChange != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("circuitbreaker onStateChange panic",
						"node", addr, "from", from, "to", to, "panic", r)
				}
			}()
			b.cfg.onStateChange(addr, from, to)
		}()
	}
}

// NodeStats 单个节点的熔断状态快照。
type NodeStats struct {
	Addr                 string `json:"addr"`
	State                State  `json:"state"`
	ConsecutiveFailures  uint32 `json:"consecutive_failures"`
	ConsecutiveSuccesses uint32 `json:"consecutive_successes"`
}

// Stats 返回所有节点的状态快照。
func (b *NodeBreaker) Stats() map[string]NodeStats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]NodeStats, len(b.nodes))
	for addr, ns := range b.nodes {
		ns.mu.Lock()
		out[addr] = NodeStats{
			Addr:                 addr,
			State:                ns.state,
			ConsecutiveFailures:  ns.consecutiveFailures,
			ConsecutiveSuccesses: ns.consecutiveSuccesses,
		}
		ns.mu.Unlock()
	}
	return out
}

// Reset 重置所有节点到 Closed。
func (b *NodeBreaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ns := range b.nodes {
		ns.mu.Lock()
		ns.state = StateClosed
		ns.consecutiveFailures = 0
		ns.consecutiveSuccesses = 0
		ns.halfOpenInflight = false
		ns.mu.Unlock()
	}
}

var _ CircuitBreaker = NoopBreaker{}
var _ CircuitBreaker = (*NodeBreaker)(nil)
