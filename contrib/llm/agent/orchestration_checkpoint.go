package agent

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// OrchestratorCheckpoint 为 Chain/Team/Parallel 提供与 Runner 一致的 opt-in checkpoint 语义。
// Store 实现 CheckpointStore 时自动写入事件;否则全部 no-op。
type OrchestratorCheckpoint struct {
	Store RunStore
	Name  string
}

func (o OrchestratorCheckpoint) enabled() bool {
	return asCheckpointStore(o.Store) != nil
}

func asCheckpointStore(store RunStore) CheckpointStore {
	if cs, ok := store.(CheckpointStore); ok {
		return cs
	}
	return nil
}

func saveSnapshotWithCheckpoint(ctx context.Context, store RunStore, runID string, snap *RunSnapshot) error {
	cs := asCheckpointStore(store)
	if cs != nil {
		n, err := cs.EventCount(ctx, runID)
		if err != nil {
			return err
		}
		snap.EventCount = n
		if snap.Kind == "" || snap.Kind == "runner" {
			snap.Messages = nil
		}
	}
	return store.Save(ctx, runID, snap)
}

func loadSnapshotWithCheckpoint(ctx context.Context, store RunStore, runID string, snap *RunSnapshot) (*RunSnapshot, error) {
	if snap == nil {
		return nil, nil
	}
	cs := asCheckpointStore(store)
	if cs == nil || snap.EventCount <= 0 || len(snap.Messages) > 0 {
		return snap, nil
	}
	if snap.Kind != "" && snap.Kind != "runner" {
		return snap, nil
	}
	events, err := cs.LoadEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	end := snap.EventCount
	if end > len(events) {
		end = len(events)
	}
	snap.Messages = checkpoint.ReplayMessages(events[:end])
	return snap, nil
}

func loadSnapshotFromStore(ctx context.Context, store RunStore, runID string) (*RunSnapshot, error) {
	snap, err := store.Load(ctx, runID)
	if err != nil {
		return nil, err
	}
	return loadSnapshotWithCheckpoint(ctx, store, runID, snap)
}

// LoadRunTreeFromStore 从 Store 的事件日志构建 sub-agent 编排树。
func LoadRunTreeFromStore(ctx context.Context, store RunStore, runID string) (*checkpoint.RunNode, error) {
	cs := asCheckpointStore(store)
	if cs == nil {
		return nil, nil
	}
	events, err := cs.LoadEvents(ctx, runID)
	if err != nil {
		return nil, err
	}
	return checkpoint.BuildRunTree(events), nil
}

// LoadUIEventsFromStore 读取 run 的全部 checkpoint/UI 事件。
func LoadUIEventsFromStore(ctx context.Context, store RunStore, runID string) ([]checkpoint.Event, error) {
	cs := asCheckpointStore(store)
	if cs == nil {
		return nil, nil
	}
	return cs.LoadEvents(ctx, runID)
}

func (o OrchestratorCheckpoint) append(ctx context.Context, runID string, events ...checkpoint.Event) {
	cs := asCheckpointStore(o.Store)
	if cs == nil || len(events) == 0 {
		return
	}
	frame := checkpoint.FrameFrom(ctx)
	name := o.Name
	if frame.AgentName != "" {
		name = frame.AgentName
	}
	for i := range events {
		if events[i].RunID == "" {
			events[i].RunID = runID
		}
		if events[i].AgentName == "" {
			events[i].AgentName = name
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

func (o OrchestratorCheckpoint) Started(ctx context.Context, runID string, req llm.Request) {
	if !o.enabled() {
		return
	}
	o.append(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunStarted, runID))
	for _, m := range req.Messages {
		if m.Role != llm.User {
			continue
		}
		ev := checkpoint.NewEvent(checkpoint.TypeUserMessage, runID)
		msg := m
		ev.Message = &msg
		o.append(ctx, runID, ev)
	}
}

func (o OrchestratorCheckpoint) Spawned(ctx context.Context, runID, childRunID, source string, step int) {
	if !o.enabled() || childRunID == "" {
		return
	}
	ev := checkpoint.NewEvent(checkpoint.TypeAgentSpawned, runID).WithStep(step)
	ev.ChildRunID = childRunID
	ev.Source = source
	o.append(ctx, runID, ev)
}

func (o OrchestratorCheckpoint) Paused(ctx context.Context, runID string, step int, resp *llm.Response, reqs []Requirement, childRunID, source string) {
	if !o.enabled() {
		return
	}
	ev := checkpoint.NewEvent(checkpoint.TypeRunPaused, runID).WithStep(step)
	ev.Response = resp
	ev.Requirements = requirementsToUI(reqs)
	ev.ChildRunID = childRunID
	ev.Source = source
	o.append(ctx, runID, ev)
}

func (o OrchestratorCheckpoint) Resumed(ctx context.Context, runID string) {
	if !o.enabled() {
		return
	}
	o.append(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunResumed, runID))
}

func (o OrchestratorCheckpoint) Completed(ctx context.Context, runID string) {
	if !o.enabled() {
		return
	}
	o.append(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunCompleted, runID))
}

func (o OrchestratorCheckpoint) Handoff(ctx context.Context, runID, from, to string, step int) {
	if !o.enabled() {
		return
	}
	ev := checkpoint.NewEvent(checkpoint.TypeAgentHandoff, runID).WithStep(step)
	ev.AgentName = from
	ev.Source = to
	o.append(ctx, runID, ev)
}

// AgentEventToCheckpoint 把 RunStream 事件转为 checkpoint.Event(SSE/UI 回放)。
func AgentEventToCheckpoint(e Event, frame checkpoint.Frame) checkpoint.Event {
	ev := checkpoint.NewEvent("", e.RunID)
	ev = ev.WithFrame(frame.ParentRunID, frame.AgentName, frame.Depth).WithStep(e.Step)
	ev.Response = e.Response
	ev.ToolCall = e.ToolCall
	ev.Result = e.Result
	ev.Requirements = requirementsToUI(e.Requirements)
	if e.Err != nil {
		ev.Error = e.Err.Error()
	}
	if e.AgentName != "" {
		ev.AgentName = e.AgentName
	}
	switch e.Type {
	case EventToken:
		ev.Type = checkpoint.TypeTokenDelta
		ev.Delta = e.Result
	case EventSteer:
		ev.Type = checkpoint.TypeSteerMessage
		ev.Message = &llm.Message{Role: llm.User, Content: e.Result}
	case EventStep:
		ev.Type = checkpoint.TypeRunStep
	case EventToolStart:
		ev.Type = checkpoint.TypeToolStart
	case EventToolResult:
		ev.Type = checkpoint.TypeToolResult
	case EventPaused:
		ev.Type = checkpoint.TypeRunPaused
	case EventFinal:
		ev.Type = checkpoint.TypeRunCompleted
	case EventError:
		ev.Type = checkpoint.TypeRunError
	default:
		ev.Type = checkpoint.TypeRunStep
	}
	return ev
}
