package session_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
)

func TestManagerRecordsSessionEvents(t *testing.T) {
	store := session.NewMemoryStore()
	mgr := &session.Manager{Store: store}
	r := &agent.Runner{
		Client: &stubClient{content: "reply"},
	}
	out := mgr.Run(context.Background(), "s1", r, llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.User, Content: "hello"}},
	})
	if !out.IsDone() {
		t.Fatalf("expected done, got %s err=%v", out.Status, out.Err)
	}
	events, err := store.LoadEvents(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("expected user+assistant events, got %d", len(events))
	}
}

type stubClient struct {
	content string
}

func (c *stubClient) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.content}, nil
}

func (c *stubClient) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 1)
	ch <- llm.Chunk{Delta: c.content}
	close(ch)
	return ch, nil
}
