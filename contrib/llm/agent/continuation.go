package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"iter"
	"log/slog"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
)

// ContinuationToken 封装一个可序列化的令牌,用于暂停/恢复/轮询长时运行的 agent 任务。
// 客户端(如 HTTP handler)可以:
//  1. 启动 RunAsync → 拿到 token
//  2. 用 store.Poll(token) 轮询结果
//  3. 任务完成或超时后,token 可被 Delete 清理

// ContinuationState 表示异步任务的当前状态。
type ContinuationState int

const (
	ContinuationRunning   ContinuationState = iota // 任务仍在运行
	ContinuationCompleted                          // 任务已完成
	ContinuationFailed                             // 任务失败
	ContinuationPaused                             // 任务暂停(需审批)
)

// ContinuationResult 是异步任务的结果快照。
type ContinuationResult struct {
	Token        string            `json:"token"`
	State        ContinuationState `json:"state"`
	Outcome      *RunOutcome       `json:"outcome,omitempty"`      // 完成/失败时有值
	Events       []Event           `json:"events,omitempty"`       // 累积的事件(可选)
	Requirements []Requirement     `json:"requirements,omitempty"` // 暂停时的审批需求
}

// ContinuationStore 管理异步任务的生命周期。
type ContinuationStore interface {
	// Start 创建一个新的 continuation,返回 token。
	Start(ctx context.Context) (token string, err error)
	// Update 更新任务状态。
	Update(ctx context.Context, token string, result ContinuationResult) error
	// Poll 查询任务当前状态。
	Poll(ctx context.Context, token string) (*ContinuationResult, error)
	// Delete 清理已完成的任务。
	Delete(ctx context.Context, token string) error
}

// MemoryContinuationStore 是内存实现的 ContinuationStore。
type MemoryContinuationStore struct {
	mu sync.RWMutex
	m  map[string]*ContinuationResult
}

// NewMemoryContinuationStore 创建空的内存 ContinuationStore。
func NewMemoryContinuationStore() *MemoryContinuationStore {
	return &MemoryContinuationStore{m: make(map[string]*ContinuationResult)}
}

// Start 生成 token 并以 Running 状态写入 store。
func (s *MemoryContinuationStore) Start(_ context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("agent: nil ContinuationStore")
	}
	token, err := newContinuationToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[token] = &ContinuationResult{Token: token, State: ContinuationRunning}
	return token, nil
}

// Update 替换指定 token 的快照(深拷贝存入)。
func (s *MemoryContinuationStore) Update(_ context.Context, token string, result ContinuationResult) error {
	if s == nil {
		return fmt.Errorf("agent: nil ContinuationStore")
	}
	if token == "" {
		return fmt.Errorf("agent: Update requires token")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[token]; !ok {
		return fmt.Errorf("agent: continuation token %q not found", token)
	}
	result.Token = token
	s.m[token] = cloneContinuationResult(&result)
	return nil
}

// Poll 返回 token 对应快照的深拷贝。
func (s *MemoryContinuationStore) Poll(_ context.Context, token string) (*ContinuationResult, error) {
	if s == nil {
		return nil, fmt.Errorf("agent: nil ContinuationStore")
	}
	if token == "" {
		return nil, fmt.Errorf("agent: Poll requires token")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result, ok := s.m[token]
	if !ok {
		return nil, fmt.Errorf("agent: continuation token %q not found", token)
	}
	return cloneContinuationResult(result), nil
}

// Delete 移除指定 token。
func (s *MemoryContinuationStore) Delete(_ context.Context, token string) error {
	if s == nil {
		return fmt.Errorf("agent: nil ContinuationStore")
	}
	if token == "" {
		return fmt.Errorf("agent: Delete requires token")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[token]; !ok {
		return fmt.Errorf("agent: continuation token %q not found", token)
	}
	delete(s.m, token)
	return nil
}

func newContinuationToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("agent: generate continuation token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func cloneContinuationResult(r *ContinuationResult) *ContinuationResult {
	if r == nil {
		return nil
	}
	out := *r
	if r.Events != nil {
		out.Events = append([]Event(nil), r.Events...)
	}
	if r.Requirements != nil {
		out.Requirements = append([]Requirement(nil), r.Requirements...)
	}
	if r.Outcome != nil {
		o := *r.Outcome
		o.Messages = cloneMessages(r.Outcome.Messages)
		if r.Outcome.Requirements != nil {
			o.Requirements = append([]Requirement(nil), r.Outcome.Requirements...)
		}
		out.Outcome = &o
	}
	return &out
}

// RunAsync 在后台 goroutine 中运行 agent,立即返回 continuation token。
// 调用方可通过 store.Poll 查询结果。
func RunAsync(ctx context.Context, store ContinuationStore, a Agent, req llm.Request, opts ...Option) (string, error) {
	if store == nil {
		return "", fmt.Errorf("agent: nil ContinuationStore")
	}
	if a == nil {
		return "", fmt.Errorf("agent: nil Agent")
	}
	token, err := store.Start(ctx)
	if err != nil {
		return "", err
	}
	bgCtx := context.WithoutCancel(ctx)
	go runAsyncLoop(bgCtx, store, token, a.Run(bgCtx, req, opts...))
	return token, nil
}

// ContinueAsync 在后台恢复暂停的 agent 任务,返回新的 continuation token。
// 调用方应在调用后删除旧 token: store.Delete(ctx, oldToken)。
func ContinueAsync(ctx context.Context, store ContinuationStore, a Agent, runID string, resolutions []Resolution, opts ...Option) (string, error) {
	if store == nil {
		return "", fmt.Errorf("agent: nil ContinuationStore")
	}
	if a == nil {
		return "", fmt.Errorf("agent: nil Agent")
	}
	token, err := store.Start(ctx)
	if err != nil {
		return "", err
	}
	bgCtx := context.WithoutCancel(ctx)
	go runAsyncLoop(bgCtx, store, token, a.Continue(bgCtx, runID, resolutions, opts...))
	return token, nil
}

func runAsyncLoop(ctx context.Context, store ContinuationStore, token string, seq iter.Seq2[Event, error]) {
	defer func() {
		if r := recover(); r != nil {
			if err := store.Update(ctx, token, ContinuationResult{
				Token: token,
				State: ContinuationFailed,
				Outcome: &RunOutcome{
					Status: StatusError,
					Err:    fmt.Errorf("agent: async run panic: %v", r),
				},
			}); err != nil {
				slog.ErrorContext(ctx, "continuation: store update failed", "token", token, "error", err)
			}
		}
	}()

	var events []Event
	var last *llm.Response
	var runID string

	updateTerminal := func(state ContinuationState, outcome *RunOutcome, reqs []Requirement) {
		if err := store.Update(ctx, token, ContinuationResult{
			Token:        token,
			State:        state,
			Outcome:      outcome,
			Events:       events,
			Requirements: reqs,
		}); err != nil {
			slog.ErrorContext(ctx, "continuation: store update failed", "token", token, "error", err)
		}
	}

	for ev, err := range seq {
		if err != nil {
			out := outcomeError(runID, last, nil, err)
			updateTerminal(ContinuationFailed, &out, nil)
			return
		}
		events = append(events, ev)
		switch ev.Type {
		case EventFinal:
			out := outcomeDone(ev.RunID, ev.Response, nil)
			updateTerminal(ContinuationCompleted, &out, nil)
			return
		case EventPaused:
			out := outcomePaused(ev.RunID, ev.Response, nil, ev.Requirements)
			updateTerminal(ContinuationPaused, &out, ev.Requirements)
			return
		case EventError:
			out := outcomeError(ev.RunID, ev.Response, nil, ev.Err)
			updateTerminal(ContinuationFailed, &out, nil)
			return
		case EventStep:
			last = ev.Response
			runID = ev.RunID
		}
		if err := store.Update(ctx, token, ContinuationResult{
			Token: token,
			State: ContinuationRunning,
		}); err != nil {
			slog.ErrorContext(ctx, "continuation: store update failed", "token", token, "error", err)
		}
	}
	out := outcomeDone(runID, last, nil)
	updateTerminal(ContinuationCompleted, &out, nil)
}
