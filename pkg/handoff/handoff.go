// Package handoff 提供跨进程实体迁移的协议原语,用于 MMO 无缝大世界场景下
// 玩家跨服务器边界时的实体所有权转移。
//
// 解决的问题:一个宏大的无缝大世界被切分成多个网格,由不同服务器进程分管。当玩家
// 跑过网格边界时,该玩家的实体必须从源进程"迁移"到目标进程,且迁移期间不能丢失
// 任何状态或操作。这涉及三个难题:
//   - 所有权唯一性:任一时刻,实体有且仅有一个进程拥有其写权限;
//   - 状态完整性:迁移期间积累的操作不能丢失;
//   - 无感连续性:玩家不应感知到"卡顿"或"加载画面"。
//
// 核心机制(协议状态机,不管传输):
//
//		         源进程                    目标进程
//		   ┌──────────────┐          ┌──────────────┐
//		   │  Owned       │          │              │
//		   │    ↓ Begin   │          │              │
//		   │  Exporting   │──Export──▶│  Importing   │
//		   │  (buffer ops)│          │  (apply)     │
//		   │    ↓ Ack     │◀──Ack───│    ↓         │
//		   │  Released    │          │  Owned       │
//		   └──────────────┘          └──────────────┘
//
//	  - Begin:源进程标记实体为"正在导出",开始缓冲后续操作(不再本地执行);
//	  - Export:源进程导出实体状态快照 + 缓冲的操作,发给目标进程;
//	  - Import:目标进程接收快照,应用缓冲操作,获得所有权;
//	  - Ack:目标进程确认接管完毕,源进程释放实体。
//
// 本包只提供协议状态机 + 操作缓冲机制,不管:
//   - 网络传输:Export 的序列化/RPC/消息队列由业务实现;
//   - 路由决策:何时迁移、迁移到哪个进程由上层(如 shard/spatial)决定;
//   - 实体类型:泛型参数 S 是实体状态,C 是操作,由业务定义。
//
// 与相邻原语的关系:
//   - shard 做一致性哈希路由(请求路由到哪个进程),handoff 做实体所有权转移;
//   - replicate 做 AOI 内的状态同步(多个观察者看到),handoff 做写权限的独占转移;
//   - match 是进程内的 actor 模型,handoff 是跨进程的 actor 迁移。
//
// 并发安全:Source/Target 各自用 sync.Mutex 保护(迁移期间可能有并发操作到达)。
// 零值不可用:用 NewSource / NewTarget 构造。
package handoff

import (
	"errors"
	"sync"
)

// Phase 迁移阶段。
type Phase int

const (
	// PhaseOwned 正常持有,本地处理所有操作。
	PhaseOwned Phase = iota
	// PhaseExporting 正在导出:操作被缓冲而非本地执行。
	PhaseExporting
	// PhaseReleased 已释放:实体不再属于本进程。
	PhaseReleased
	// PhaseImporting 正在导入:目标进程接收状态。
	PhaseImporting
)

func (p Phase) String() string {
	switch p {
	case PhaseOwned:
		return "owned"
	case PhaseExporting:
		return "exporting"
	case PhaseReleased:
		return "released"
	case PhaseImporting:
		return "importing"
	default:
		return "unknown"
	}
}

var (
	ErrNotOwned      = errors.New("handoff: entity not owned by this process")
	ErrAlreadyExport = errors.New("handoff: entity already exporting")
	ErrNotExporting  = errors.New("handoff: entity not in exporting phase")
	ErrNotImporting  = errors.New("handoff: entity not in importing phase")
	ErrReleased      = errors.New("handoff: entity already released")
)

// Bundle 是迁移包:实体状态快照 + 导出期间缓冲的操作。
// 由源进程 Export 产出,目标进程 Import 消费。
type Bundle[S, C any] struct {
	// State 实体状态快照(迁移起始时刻的完整状态)。
	State S
	// Buffered 导出期间缓冲的操作(按到达顺序)。
	// 目标进程应在 Import 后按序重放这些操作。
	Buffered []C
}

// ---------- Source(源进程) ----------

// Source 管理实体在源进程的迁移生命周期。
type Source[S, C any] struct {
	mu     sync.Mutex
	phase  Phase
	state  S
	buffer []C
}

// NewSource 创建源端管理器。state 是实体当前状态。
func NewSource[S, C any](state S) *Source[S, C] {
	return &Source[S, C]{phase: PhaseOwned, state: state}
}

// Phase 返回当前阶段。
func (s *Source[S, C]) Phase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// Begin 开始导出:从此刻起,新到达的操作被缓冲而非本地执行。
// 调用方应在此之后尽快调用 Export 导出迁移包。
func (s *Source[S, C]) Begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.phase {
	case PhaseOwned:
		s.phase = PhaseExporting
		s.buffer = nil
		return nil
	case PhaseExporting:
		return ErrAlreadyExport
	case PhaseReleased:
		return ErrReleased
	default:
		return ErrNotOwned
	}
}

// Buffer 在导出期间缓冲一个操作。如果实体仍处于 Owned 阶段,返回 false
// (调用方应正常本地执行);如果处于 Exporting 阶段,操作被缓冲,返回 true。
// 返回 error 如果实体已 Released。
func (s *Source[S, C]) Buffer(cmd C) (buffered bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.phase {
	case PhaseOwned:
		return false, nil
	case PhaseExporting:
		s.buffer = append(s.buffer, cmd)
		return true, nil
	case PhaseReleased:
		return false, ErrReleased
	default:
		return false, ErrNotOwned
	}
}

// Export 导出迁移包(状态快照 + 缓冲操作)。调用后仍处于 Exporting 阶段,
// 等待目标进程 Ack 后调用 Release。
func (s *Source[S, C]) Export() (Bundle[S, C], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseExporting {
		return Bundle[S, C]{}, ErrNotExporting
	}
	buf := s.buffer
	s.buffer = nil
	return Bundle[S, C]{State: s.state, Buffered: buf}, nil
}

// DrainBuffer 在 Export 之后、Release 之前,取走新增的缓冲操作(增量追发)。
// 适用于导出→传输这段时间内又有新操作到达的场景。
func (s *Source[S, C]) DrainBuffer() ([]C, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseExporting {
		return nil, ErrNotExporting
	}
	buf := s.buffer
	s.buffer = nil
	return buf, nil
}

// Release 释放实体所有权(收到目标进程的 Ack 后调用)。
// 此后对该实体的任何操作都返回 ErrReleased。
func (s *Source[S, C]) Release() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseExporting {
		return ErrNotExporting
	}
	s.phase = PhaseReleased
	s.buffer = nil
	return nil
}

// Abort 中止迁移,回到 Owned 状态(迁移失败时)。缓冲的操作通过返回值交还,
// 调用方应在本地重放。
func (s *Source[S, C]) Abort() ([]C, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != PhaseExporting {
		return nil, ErrNotExporting
	}
	s.phase = PhaseOwned
	buf := s.buffer
	s.buffer = nil
	return buf, nil
}

// ---------- Target(目标进程) ----------

// Target 管理实体在目标进程的导入生命周期。
type Target[S, C any] struct {
	mu    sync.Mutex
	phase Phase
	state S
}

// NewTarget 创建目标端管理器(初始处于 Importing 阶段)。
func NewTarget[S, C any]() *Target[S, C] {
	return &Target[S, C]{phase: PhaseImporting}
}

// Phase 返回当前阶段。
func (t *Target[S, C]) Phase() Phase {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase
}

// Import 接收迁移包,应用状态快照。返回缓冲的操作列表,调用方应按序重放。
// 重放完成后调用 Accept 获得所有权。
func (t *Target[S, C]) Import(bundle Bundle[S, C]) (buffered []C, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phase != PhaseImporting {
		return nil, ErrNotImporting
	}
	t.state = bundle.State
	return bundle.Buffered, nil
}

// Accept 确认接管完毕,转为 Owned 状态。调用方应在重放完缓冲操作后调用。
func (t *Target[S, C]) Accept() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phase != PhaseImporting {
		return ErrNotImporting
	}
	t.phase = PhaseOwned
	return nil
}

// State 返回当前实体状态。
func (t *Target[S, C]) State() S {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// UpdateState 更新实体状态(重放缓冲操作时使用)。
func (t *Target[S, C]) UpdateState(state S) {
	t.mu.Lock()
	t.state = state
	t.mu.Unlock()
}
