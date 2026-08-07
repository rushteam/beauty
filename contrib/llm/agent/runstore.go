package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/rushteam/beauty/contrib/llm"
)

// RunStore 持久化暂停中的 run 快照。nil Runner.Store 时用内存实现。
type RunStore interface {
	Save(ctx context.Context, id string, snap *RunSnapshot) error
	Load(ctx context.Context, id string) (*RunSnapshot, error)
	Delete(ctx context.Context, id string) error
}

// RunSnapshot 是可恢复的暂停态。Kind 区分编排类型。
type RunSnapshot struct {
	Kind string // runner | chain | team | parallel

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

// MemoryRunStore 是并发安全的内存 RunStore。
type MemoryRunStore struct {
	mu sync.Mutex
	m  map[string]*RunSnapshot
}

// NewMemoryRunStore 创建空的内存 Store。
func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{m: make(map[string]*RunSnapshot)}
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
