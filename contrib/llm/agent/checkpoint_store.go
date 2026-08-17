package agent

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// CheckpointStore 打通 RunStore 快照与 checkpoint 事件日志。
// Pause 时 RunSnapshot.EventCount 记录事件序号;Load 时 Messages 由 Replay 重建。
type CheckpointStore interface {
	RunStore
	checkpoint.EventLog
}

// MemoryCheckpointStore 是内存版 CheckpointStore(测试/单机)。
type MemoryCheckpointStore struct {
	*MemoryRunStore
	log *checkpoint.MemoryEventLog
}

// NewMemoryCheckpointStore 创建内存 checkpoint store。
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{
		MemoryRunStore: NewMemoryRunStore(),
		log:            checkpoint.NewMemoryEventLog(),
	}
}

func (s *MemoryCheckpointStore) AppendEvents(ctx context.Context, runID string, events ...checkpoint.Event) error {
	return s.log.AppendEvents(ctx, runID, events...)
}

func (s *MemoryCheckpointStore) LoadEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	return s.log.LoadEvents(ctx, runID)
}

func (s *MemoryCheckpointStore) EventCount(ctx context.Context, runID string) (int, error) {
	return s.log.EventCount(ctx, runID)
}

// Delete 删除暂停快照;事件日志保留供 UI 回放与审计。
func (s *MemoryCheckpointStore) Delete(ctx context.Context, runID string) error {
	return s.MemoryRunStore.Delete(ctx, runID)
}

var _ CheckpointStore = (*MemoryCheckpointStore)(nil)

func (r *Runner) checkpointStore() CheckpointStore {
	return asCheckpointStore(r.Store)
}

func (r *Runner) runFrame(ctx context.Context, runID string) checkpoint.Frame {
	f := checkpoint.FrameFrom(ctx)
	if f.RunID == "" {
		f.RunID = runID
	}
	if f.AgentName == "" {
		f.AgentName = r.Name
	}
	return f
}

func (r *Runner) appendCheckpoint(ctx context.Context, runID string, events ...checkpoint.Event) {
	cs := r.checkpointStore()
	if cs == nil || len(events) == 0 {
		return
	}
	frame := r.runFrame(ctx, runID)
	for i := range events {
		if events[i].RunID == "" {
			events[i].RunID = runID
		}
		if events[i].AgentName == "" {
			events[i].AgentName = frame.AgentName
		}
		if events[i].Depth == 0 && frame.Depth > 0 {
			events[i].Depth = frame.Depth
		}
		if events[i].ParentRunID == "" {
			events[i].ParentRunID = frame.ParentRunID
		}
	}
	_ = cs.AppendEvents(ctx, runID, events...)
}

func requirementsToUI(reqs []Requirement) []checkpoint.Requirement {
	out := make([]checkpoint.Requirement, len(reqs))
	for i, rq := range reqs {
		out[i] = checkpoint.Requirement{ID: rq.ID, ToolCall: rq.ToolCall, Source: rq.Source}
	}
	return out
}

func (r *Runner) saveCheckpoint(ctx context.Context, runID string, snap *RunSnapshot) error {
	return saveSnapshotWithCheckpoint(ctx, r.Store, runID, snap)
}

func (r *Runner) loadSnapshot(ctx context.Context, runID string) (*RunSnapshot, error) {
	return loadSnapshotFromStore(ctx, r.Store, runID)
}

// LoadRunTree 从 checkpoint 事件日志构建 sub-agent 编排树(UI 可视化)。
func (r *Runner) LoadRunTree(ctx context.Context, runID string) (*checkpoint.RunNode, error) {
	return LoadRunTreeFromStore(ctx, r.Store, runID)
}

// LoadUIEvents 读取 run 的全部 UI/checkpoint 事件(HITL 前端回放)。
func (r *Runner) LoadUIEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	return LoadUIEventsFromStore(ctx, r.Store, runID)
}

func (r *Runner) emitEvent(ctx context.Context, runID string, emit func(Event), e Event) {
	if e.RunID == "" {
		e.RunID = runID
	}
	if e.AgentName == "" {
		e.AgentName = r.Name
	}
	if emit != nil {
		emit(e)
	}
	if r.checkpointStore() == nil {
		return
	}
	frame := r.runFrame(ctx, runID)
	r.appendCheckpoint(ctx, runID, AgentEventToCheckpoint(e, frame))
	if e.Type == EventStep && e.Response != nil && len(e.Response.ToolCalls) > 0 {
		r.recordModelCheckpoint(ctx, runID, e.Step, e.Response)
	}
}

func (r *Runner) recordModelCheckpoint(ctx context.Context, runID string, step int, resp *llm.Response) {
	if resp == nil || len(resp.ToolCalls) == 0 {
		return
	}
	ev := checkpoint.NewEvent(checkpoint.TypeModelResponse, runID).WithStep(step)
	ev.Response = resp
	r.appendCheckpoint(ctx, runID, ev)
}

func (r *Runner) checkpointPaused(ctx context.Context, runID string, emit func(Event), step int, resp *llm.Response, reqs []Requirement) {
	ev := checkpoint.NewEvent(checkpoint.TypeRunPaused, runID).WithStep(step)
	ev.Response = resp
	ev.Requirements = requirementsToUI(reqs)
	r.appendCheckpoint(ctx, runID, ev)
	if emit != nil {
		emit(Event{Type: EventPaused, Step: step, Response: resp, RunID: runID, Requirements: reqs})
	}
}
