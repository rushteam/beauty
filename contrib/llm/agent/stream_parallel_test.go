package agent_test

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// streamScriptClient:Stream 按预设 chunk 推送;Generate 仍可用。
type streamScriptClient struct {
	streams [][]llm.Chunk
	si      int
	gens    []*llm.Response
	gi      int
}

func (c *streamScriptClient) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	r := c.gens[c.gi]
	c.gi++
	return r, nil
}

func (c *streamScriptClient) Stream(_ context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	chunks := c.streams[c.si]
	c.si++
	return func(yield func(llm.Chunk, error) bool) {
		for _, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

func TestRunStream_Tokens(t *testing.T) {
	fc := &streamScriptClient{
		streams: [][]llm.Chunk{
			{{Delta: "Hel"}, {Delta: "lo"}},
		},
	}
	r := &agent.Runner{Client: fc}
	var tokens []string
	var final string
	for ev, err := range r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case agent.EventToken:
			tokens = append(tokens, ev.Result)
		case agent.EventFinal:
			final = ev.Response.Content
		case agent.EventError:
			t.Fatalf("err: %v", ev.Err)
		}
	}
	if strings.Join(tokens, "") != "Hello" || final != "Hello" {
		t.Fatalf("tokens=%v final=%q", tokens, final)
	}
}

func TestRunStream_StreamToolCalls(t *testing.T) {
	fc := &streamScriptClient{
		streams: [][]llm.Chunk{
			{
				{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
			},
			{{Delta: "done"}},
		},
	}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}}
	var final string
	for ev, err := range r.Run(context.Background(), llm.Request{Model: "m"}) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type == agent.EventFinal {
			final = ev.Response.Content
		}
		if ev.Type == agent.EventError {
			t.Fatal(ev.Err)
		}
	}
	if final != "done" {
		t.Fatalf("final=%q", final)
	}
}

func TestParallelTools(t *testing.T) {
	var concurrent int32
	var maxConcurrent int32
	slow := func(name string) agent.Tool {
		return agent.Func(name, "", nil, func(context.Context, json.RawMessage) (string, error) {
			n := atomic.AddInt32(&concurrent, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if n <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
			return name + "-ok", nil
		})
	}
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "a", Arguments: json.RawMessage(`{}`)},
			{ID: "2", Name: "b", Arguments: json.RawMessage(`{}`)},
		}},
		{Content: "both"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{slow("a"), slow("b")}}
	start := time.Now()
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "both" {
		t.Fatalf("content=%q", resp.Content)
	}
	if atomic.LoadInt32(&maxConcurrent) < 2 {
		t.Fatalf("expected parallel, maxConcurrent=%d", maxConcurrent)
	}
	if time.Since(start) > 150*time.Millisecond {
		// 串行会 ~100ms+,并行 ~50ms;留余量
		t.Fatalf("too slow for parallel: %s", time.Since(start))
	}
	// 结果顺序应与 tool_call 顺序一致
	msgs := fc.lastReq.Messages
	var toolContents []string
	for _, m := range msgs {
		if m.Role == llm.Tool {
			toolContents = append(toolContents, m.Content)
		}
	}
	if len(toolContents) != 2 || toolContents[0] != "a-ok" || toolContents[1] != "b-ok" {
		t.Fatalf("order=%v", toolContents)
	}
}

func TestParallelTools_Disabled(t *testing.T) {
	var mu sync.Mutex
	var order []string
	mk := func(name string) agent.Tool {
		return agent.Func(name, "", nil, func(context.Context, json.RawMessage) (string, error) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			return name, nil
		})
	}
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "a"}, {ID: "2", Name: "b"},
		}},
		{Content: "ok"},
	}}
	r := &agent.Runner{
		Client:        fc,
		Tools:         []agent.Tool{mk("a"), mk("b")},
		ParallelTools: agent.Bool(false),
	}
	if _, err := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"})).Final(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("serial order=%v", order)
	}
}
