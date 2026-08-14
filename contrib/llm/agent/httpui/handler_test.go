package httpui_test

import (
	"bufio"
	"context"
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

func (s stubAgent) Run(ctx context.Context, req llm.Request) agent.RunOutcome {
	return agent.RunOutcome{
		Status:   agent.StatusDone,
		RunID:    "run-test",
		Response: &llm.Response{Content: "ok"},
	}
}

func (s stubAgent) Continue(ctx context.Context, runID string, resolutions []agent.Resolution) agent.RunOutcome {
	return s.Run(ctx, llm.Request{})
}

func (s stubAgent) RunStream(ctx context.Context, req llm.Request) <-chan agent.Event {
	ch := make(chan agent.Event, 2)
	go func() {
		defer close(ch)
		ch <- agent.Event{Type: agent.EventStep, RunID: "run-test", Response: &llm.Response{Content: "hi"}}
		ch <- agent.Event{Type: agent.EventFinal, RunID: "run-test", Response: &llm.Response{Content: "ok"}}
	}()
	return ch
}

func (s stubAgent) ContinueStream(ctx context.Context, runID string, resolutions []agent.Resolution) <-chan agent.Event {
	return s.RunStream(ctx, llm.Request{})
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
