package agui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
)

// PatchOp 是一条 RFC 6902 操作。这里只生成 add / remove / replace。
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"-"`
}

func (p PatchOp) MarshalJSON() ([]byte, error) {
	m := map[string]any{"op": p.Op, "path": p.Path}
	if p.Op != "remove" {
		m["value"] = p.Value
	}
	return json.Marshal(m)
}

func (e Event) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": e.Type}
	put := func(key, val string) {
		if val != "" {
			m[key] = val
		}
	}
	put("threadId", e.ThreadID)
	put("runId", e.RunID)
	if e.Timestamp != 0 {
		m["timestamp"] = e.Timestamp
	}
	put("messageId", e.MessageID)
	put("role", e.Role)
	put("toolCallId", e.ToolCallID)
	put("toolCallName", e.ToolCallName)
	put("message", e.Message)
	put("code", e.ErrorCode)
	put("stepName", e.StepName)
	put("content", e.Content)
	put("result", e.Result)
	if e.Type == EventStateDelta && len(e.Patch) > 0 {
		m["delta"] = e.Patch
	} else if e.Delta != "" {
		m["delta"] = e.Delta
	}
	if len(e.Snapshot) > 0 {
		m["snapshot"] = json.RawMessage(e.Snapshot)
	}
	return json.Marshal(m)
}

func (e *Event) UnmarshalJSON(b []byte) error {
	var raw struct {
		Type         EventType       `json:"type"`
		ThreadID     string          `json:"threadId"`
		RunID        string          `json:"runId"`
		Timestamp    int64           `json:"timestamp"`
		MessageID    string          `json:"messageId"`
		Role         string          `json:"role"`
		Delta        json.RawMessage `json:"delta"`
		ToolCallID   string          `json:"toolCallId"`
		ToolCallName string          `json:"toolCallName"`
		Message      string          `json:"message"`
		ErrorCode    string          `json:"code"`
		StepName     string          `json:"stepName"`
		Snapshot     json.RawMessage `json:"snapshot"`
		Content      string          `json:"content"`
		Result       string          `json:"result"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*e = Event{
		Type: raw.Type, ThreadID: raw.ThreadID, RunID: raw.RunID, Timestamp: raw.Timestamp,
		MessageID: raw.MessageID, Role: raw.Role, ToolCallID: raw.ToolCallID, ToolCallName: raw.ToolCallName,
		Message: raw.Message, ErrorCode: raw.ErrorCode, StepName: raw.StepName,
		Snapshot: raw.Snapshot, Content: raw.Content, Result: raw.Result,
	}
	if len(raw.Delta) == 0 {
		return nil
	}
	switch raw.Delta[0] {
	case '"':
		return json.Unmarshal(raw.Delta, &e.Delta)
	case '[':
		return json.Unmarshal(raw.Delta, &e.Patch)
	default:
		return nil
	}
}

// stateEvent 比较该 thread 上一次快照。第一次发整份快照,之后只发非空差量。
// 多副本共享 StateStore 时，CAS 冲突后重读并重算补丁，避免以旧基线覆盖新状态。
func (h *Handler) stateEvent(ctx context.Context, threadID string, raw json.RawMessage) (Event, bool) {
	canon, ok := canonicalState(raw)
	if !ok {
		return Event{}, false
	}
	for attempts := 0; attempts < 4; attempts++ {
		prev, seen, err := h.stateStore.Load(ctx, threadID)
		if err != nil {
			return Event{}, false
		}
		if seen && bytes.Equal(prev.State, canon) {
			return Event{}, false
		}
		version := uint64(0)
		if seen {
			version = prev.Version
		}
		if _, err := h.stateStore.CompareAndSwap(ctx, threadID, version, canon); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return Event{}, false
		}
		if !seen {
			return Event{Type: EventStateSnapshot, ThreadID: threadID, Snapshot: canon}, true
		}
		ops := diffState(prev.State, canon)
		if len(ops) == 0 {
			return Event{}, false
		}
		return Event{Type: EventStateDelta, ThreadID: threadID, Patch: ops}, true
	}
	return Event{}, false
}

func canonicalState(raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return out, true
}

func diffState(prev, next json.RawMessage) []PatchOp {
	var a, b any
	if err := json.Unmarshal(prev, &a); err != nil {
		return nil
	}
	if err := json.Unmarshal(next, &b); err != nil {
		return nil
	}
	var ops []PatchOp
	diffValue("", a, b, &ops)
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path == ops[j].Path {
			return ops[i].Op < ops[j].Op
		}
		return ops[i].Path < ops[j].Path
	})
	return ops
}

func diffValue(path string, a, b any, ops *[]PatchOp) {
	if reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := make([]string, 0, len(am)+len(bm))
		seen := map[string]struct{}{}
		for k := range am {
			keys = append(keys, k)
			seen[k] = struct{}{}
		}
		for k := range bm {
			if _, ok := seen[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := path + "/" + escapePointer(k)
			av, aHas := am[k]
			bv, bHas := bm[k]
			switch {
			case aHas && !bHas:
				*ops = append(*ops, PatchOp{Op: "remove", Path: p})
			case !aHas && bHas:
				*ops = append(*ops, PatchOp{Op: "add", Path: p, Value: bv})
			default:
				diffValue(p, av, bv, ops)
			}
		}
		return
	}
	as, aok := a.([]any)
	bs, bok := b.([]any)
	if aok && bok && len(as) == len(bs) {
		for i := range bs {
			diffValue(path+"/"+strconv.Itoa(i), as[i], bs[i], ops)
		}
		return
	}
	*ops = append(*ops, PatchOp{Op: "replace", Path: path, Value: b})
}

func escapePointer(token string) string {
	out := make([]byte, 0, len(token))
	for i := 0; i < len(token); i++ {
		switch token[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, token[i])
		}
	}
	return string(out)
}
