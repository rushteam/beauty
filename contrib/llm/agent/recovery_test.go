package agent_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type scriptErrClient struct {
	errs    []error
	ok      *llm.Response
	calls   int
	lastReq llm.Request
	maxSeen int
}

func (c *scriptErrClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.lastReq = req
	c.calls++
	if req.MaxTokens > c.maxSeen {
		c.maxSeen = req.MaxTokens
	}
	if c.calls <= len(c.errs) && c.errs[c.calls-1] != nil {
		return nil, c.errs[c.calls-1]
	}
	if c.ok != nil {
		return c.ok, nil
	}
	return &llm.Response{Content: "ok"}, nil
}

func (c *scriptErrClient) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

func TestRecovery_MaxOutputBumpsTokens(t *testing.T) {
	c := &scriptErrClient{
		errs: []error{errors.New("max_output_tokens exceeded")},
		ok:   &llm.Response{Content: "recovered"},
	}
	r := &agent.Runner{
		Client:   c,
		Recovery: &agent.Recovery{MaxAttempts: 2, MaxTokensBump: 1024},
	}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m", MaxTokens: 100}))
	resp, err := out.Final()
	if err != nil || resp.Content != "recovered" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if c.calls != 2 {
		t.Fatalf("calls=%d want 2", c.calls)
	}
	if c.maxSeen != 100+1024 {
		t.Fatalf("MaxTokens=%d want %d", c.maxSeen, 1124)
	}
}

func TestRecovery_PromptTooLongCompacts(t *testing.T) {
	c := &scriptErrClient{
		errs: []error{errors.New("prompt_too_long: context_length_exceeded")},
		ok:   &llm.Response{Content: "after-compact"},
	}
	r := &agent.Runner{Client: c}
	big := stringsRepeat("x", 20000)
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.User, Content: "q"},
			{Role: llm.Tool, ToolCallID: "t1", Content: big},
		},
	}))
	resp, err := out.Final()
	if err != nil || resp.Content != "after-compact" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if c.calls != 2 {
		t.Fatalf("calls=%d want 2", c.calls)
	}
	var tool string
	for _, m := range c.lastReq.Messages {
		if m.Role == llm.Tool {
			tool = m.Content
		}
	}
	if tool == big {
		t.Fatal("overflow recovery should snip/microcompact tool result")
	}
}

func TestRecovery_OtherErrorsNotRetried(t *testing.T) {
	want := errors.New("HTTP 401 unauthorized")
	c := &scriptErrClient{errs: []error{want}}
	r := &agent.Runner{Client: c}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if out.Status != agent.StatusError || !errors.Is(out.Err, want) {
		t.Fatalf("status=%s err=%v", out.Status, out.Err)
	}
	if c.calls != 1 {
		t.Fatalf("calls=%d want 1", c.calls)
	}
}

func stringsRepeat(s string, n int) string {
	b := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}
