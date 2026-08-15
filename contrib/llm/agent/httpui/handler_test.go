package httpui_test

import (
	"bufio"
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
	"github.com/rushteam/beauty/contrib/llm/agent/httpui"
)

type stubAgent struct {
	name string
}

func (s stubAgent) Info() agent.Info { return agent.Info{Name: s.name} }

func (s stubAgent) Run(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		yield(agent.Event{Type: agent.EventStep, RunID: "run-test", Response: &llm.Response{Content: "hi"}}, nil)
		yield(agent.Event{Type: agent.EventFinal, RunID: "run-test", Response: &llm.Response{Content: "ok"}}, nil)
	}
}

func (s stubAgent) Continue(_ context.Context, _ string, _ []agent.Resolution, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return s.Run(context.Background(), llm.Request{})
}

func TestHandlerRunSSE(t *testing.T) {
	store := agent.NewMemoryCheckpointStore()
	h := &httpui.Handler{Agent: stubAgent{name: "demo"}, Store: store}
	mux := http.NewServeMux()
	mux.Handle("/", h)

	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	sc := bufio.NewScanner(rec.Body)
	var lines int
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") {
			lines++
		}
	}
	if lines < 2 {
		t.Fatalf("expected >=2 SSE data lines, got %d", lines)
	}
}

func TestReplayEvents(t *testing.T) {
	store := agent.NewMemoryCheckpointStore()
	ctx := context.Background()
	_ = store.AppendEvents(ctx, "run-1", agent.AgentEventToCheckpoint(
		agent.Event{Type: agent.EventFinal, RunID: "run-1"},
		checkpoint.Frame{},
	))

	h := &httpui.Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/events?run_id=run-1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}
