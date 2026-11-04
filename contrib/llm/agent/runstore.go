package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// RunStore 持久化暂停中的 run 快照。nil Runner.Store 时用内存实现。
type RunStore interface {
	Save(ctx context.Context, id string, snap *RunSnapshot) error
	Load(ctx context.Context, id string) (*RunSnapshot, error)
	Delete(ctx context.Context, id string) error
}

var (
	// ErrRunClaimed 表示已有其他 worker 正在恢复该 run。
	ErrRunClaimed = errors.New("agent: run is already claimed")
	// ErrLeaseLost 表示 lease 已过期、被接管，或令牌不匹配。
	ErrLeaseLost = errors.New("agent: run lease lost")
)

// RunLease 是 RunCoordinator 返回的排他恢复凭证。Token 必须视为不透明值。
type RunLease struct {
	Token     string
	ExpiresAt time.Time
}

// RunCoordinator 给暂停 run 提供跨副本的排他恢复协调。
//
// Claim、Renew、Release 必须是原子的。Claim 成功后，只有持有同一 Token 的调用方
// 能续约或释放；TTL 到期后允许其他 worker 接管。Redis 可用 SET NX PX + Lua，
// SQL 可用带 expires_at 条件的 UPDATE/INSERT 实现。
//
// Runner 发现 Store 同时实现本接口时，会在 Continue 期间自动 claim。普通 RunStore
// 仍兼容，但只适合调用方保证同一 run 单写者的场景。
type RunCoordinator interface {
	Claim(ctx context.Context, runID, owner string, ttl time.Duration) (RunLease, error)
	Renew(ctx context.Context, runID string, lease RunLease, ttl time.Duration) (RunLease, error)
	Release(ctx context.Context, runID string, lease RunLease) error
}

// RunSnapshot 是可恢复的暂停态。Kind 区分编排类型。
type RunSnapshot struct {
	Kind string // runner | chain | team | parallel

	// EventCount 是 checkpoint 模式下已写入事件日志的条数。
	// Load 时 Messages = Replay(events[:EventCount]);非 checkpoint 模式为 0 且 Messages 内联存储。
	EventCount int

	// runner
	Request      llm.Request
	Messages     []llm.Message
	PendingTCs   []llm.ToolCall // 本轮全部 tool_calls(原子暂停,尚未执行)
	Requirements []Requirement
	Step         int

	// 嵌套子 run(AgentAsTool / chain step / team member / parallel branch)
	ChildRunID  string
	ChildSource string

	// chain
	ChainStep   int
	LastContent string // 上一步终态文本,供后续 stepReq

	// team
	Member        string
	HandoffCount  int
	HandoffWindow []string

	// parallel
	BranchOutcomes map[int]RunOutcome // 已结束(Done/Error)分支;Paused 的在 Child 或单独存
	PausedBranches map[int]string     // branch index → child runID
}

// MemoryRunStore 是并发安全的内存 RunStore，仅用于开发、测试或单副本部署。
type MemoryRunStore struct {
	mu sync.Mutex
	m  map[string]*RunSnapshot
	// leases 只模拟单进程竞争，不能用于多副本生产部署。
	leases map[string]memoryLease
}

type memoryLease struct {
	token     string
	expiresAt time.Time
}

// NewMemoryRunStore 创建空的内存 Store。
func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{
		m:      make(map[string]*RunSnapshot),
		leases: make(map[string]memoryLease),
	}
}

// Save 深拷贝存入。
func (s *MemoryRunStore) Save(_ context.Context, id string, snap *RunSnapshot) error {
	if s == nil {
		return fmt.Errorf("agent: nil RunStore")
	}
	if id == "" || snap == nil {
		return fmt.Errorf("agent: Save requires id and snapshot")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = cloneSnapshot(snap)
	return nil
}

// Load 返回深拷贝;不存在时 (nil, nil)。
func (s *MemoryRunStore) Load(_ context.Context, id string) (*RunSnapshot, error) {
	if s == nil {
		return nil, fmt.Errorf("agent: nil RunStore")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.m[id]
	if !ok {
		return nil, nil
	}
	return cloneSnapshot(snap), nil
}

// Delete 删除记录。
func (s *MemoryRunStore) Delete(_ context.Context, id string) error {
	if s == nil {
		return fmt.Errorf("agent: nil RunStore")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// Claim 实现 RunCoordinator，仅用于开发、测试或单副本部署。
func (s *MemoryRunStore) Claim(_ context.Context, id, _ string, ttl time.Duration) (RunLease, error) {
	if s == nil || id == "" {
		return RunLease{}, fmt.Errorf("agent: Claim requires store and runID")
	}
	if ttl <= 0 {
		return RunLease{}, fmt.Errorf("agent: Claim requires positive ttl")
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.leases[id]; ok && existing.expiresAt.After(now) {
		return RunLease{}, ErrRunClaimed
	}
	token, err := newLeaseToken()
	if err != nil {
		return RunLease{}, err
	}
	lease := RunLease{Token: token, ExpiresAt: now.Add(ttl)}
	s.leases[id] = memoryLease{token: lease.Token, expiresAt: lease.ExpiresAt}
	return lease, nil
}

// Renew 实现 RunCoordinator。
func (s *MemoryRunStore) Renew(_ context.Context, id string, lease RunLease, ttl time.Duration) (RunLease, error) {
	if s == nil || id == "" || lease.Token == "" || ttl <= 0 {
		return RunLease{}, ErrLeaseLost
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[id]
	if !ok || current.token != lease.Token || !current.expiresAt.After(now) {
		return RunLease{}, ErrLeaseLost
	}
	renewed := RunLease{Token: lease.Token, ExpiresAt: now.Add(ttl)}
	s.leases[id] = memoryLease{token: renewed.Token, expiresAt: renewed.ExpiresAt}
	return renewed, nil
}

// Release 实现 RunCoordinator。
func (s *MemoryRunStore) Release(_ context.Context, id string, lease RunLease) error {
	if s == nil || id == "" || lease.Token == "" {
		return ErrLeaseLost
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[id]
	if !ok || current.token != lease.Token {
		return ErrLeaseLost
	}
	delete(s.leases, id)
	return nil
}

func newLeaseToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent: generate lease token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func cloneSnapshot(s *RunSnapshot) *RunSnapshot {
	if s == nil {
		return nil
	}
	out := *s
	out.Messages = cloneMessages(s.Messages)
	if s.PendingTCs != nil {
		out.PendingTCs = make([]llm.ToolCall, len(s.PendingTCs))
		copy(out.PendingTCs, s.PendingTCs)
	}
	if s.Requirements != nil {
		out.Requirements = make([]Requirement, len(s.Requirements))
		copy(out.Requirements, s.Requirements)
	}
	if s.HandoffWindow != nil {
		out.HandoffWindow = append([]string{}, s.HandoffWindow...)
	}
	if s.BranchOutcomes != nil {
		out.BranchOutcomes = make(map[int]RunOutcome, len(s.BranchOutcomes))
		for k, v := range s.BranchOutcomes {
			out.BranchOutcomes[k] = v
		}
	}
	if s.PausedBranches != nil {
		out.PausedBranches = make(map[int]string, len(s.PausedBranches))
		for k, v := range s.PausedBranches {
			out.PausedBranches[k] = v
		}
	}
	out.Request.Messages = cloneMessages(s.Request.Messages)
	if s.Request.Tools != nil {
		out.Request.Tools = append([]llm.ToolDef{}, s.Request.Tools...)
	}
	return &out
}

var runIDSeq atomic.Uint64

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := runIDSeq.Add(1)
	return fmt.Sprintf("run-%d-%s", n, hex.EncodeToString(b[:]))
}
