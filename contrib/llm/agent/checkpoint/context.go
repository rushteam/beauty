package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Frame 描述一次 run 在编排树中的位置。
type Frame struct {
	RunID       string
	ParentRunID string
	AgentName   string
	Depth       int
}

type frameKey struct{}

// WithFrame 把编排帧写入 context(子 agent 继承 parent)。
func WithFrame(ctx context.Context, f Frame) context.Context {
	return context.WithValue(ctx, frameKey{}, f)
}

// FrameFrom 读取编排帧;不存在时返回零值。
func FrameFrom(ctx context.Context) Frame {
	if v, ok := ctx.Value(frameKey{}).(Frame); ok {
		return v
	}
	return Frame{}
}

// ChildFrame 为子 run 构造编排帧。
func ChildFrame(parent Frame, childRunID, source string) Frame {
	return Frame{
		RunID:       childRunID,
		ParentRunID: parent.RunID,
		AgentName:   source,
		Depth:       parent.Depth + 1,
	}
}

// WriteSSE 将 UI 事件编码为 SSE data 行(JSON)。
func WriteSSE(w io.Writer, ev Event) error {
	if ev.Schema == "" {
		ev.Schema = SchemaVersion
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
	return err
}

// MarshalJSON 返回事件的 JSON 字节(供 WebSocket/REST)。
func MarshalJSON(ev Event) ([]byte, error) {
	if ev.Schema == "" {
		ev.Schema = SchemaVersion
	}
	return json.Marshal(ev)
}
