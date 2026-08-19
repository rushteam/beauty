package agui

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type mockAgent struct {
	events []agent.Event
}

func (m *mockAgent) Run(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		for _, ev := range m.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (m *mockAgent) Continue(_ context.Context, _ string, _ []agent.Resolution, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {}
}

func (m *mockAgent) Info() agent.Info { return agent.Info{Name: "mock"} }

func TestInputToBeautyMessages(t *testing.T) {
	input := &RunAgentInput{
		Messages: []InputMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "system", Content: "sys"},
		},
	}
	msgs := inputToBeautyMessages(input)
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d", len(msgs))
	}
	if msgs[0].Role != llm.User || msgs[1].Role != llm.Assistant || msgs[2].Role != llm.System {
		t.Fatalf("roles = %v %v %v", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
}

func TestBeautyRoleToAGUI(t *testing.T) {
	if beautyRoleToAGUI(llm.User) != "user" {
		t.Fatal("user role mismatch")
	}
	if aguiRoleToBeauty("assistant") != llm.Assistant {
		t.Fatal("assistant role mismatch")
	}
}

func TestGenerateID_HasPrefix(t *testing.T) {
	id := generateID("thread")
	if !strings.HasPrefix(id, "thread-") {
		t.Fatalf("id = %q", id)
	}
}

func TestNewHandler_RejectsNonPost(t *testing.T) {
	h := NewHandler(&mockAgent{}, HandlerConfig{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agent", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestNewHandler_InvalidJSON(t *testing.T) {
	h := NewHandler(&mockAgent{}, HandlerConfig{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agent", strings.NewReader("not-json"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_ServeHTTP_SSE(t *testing.T) {
	h := NewHandler(&mockAgent{
		events: []agent.Event{
			{Type: agent.EventToken, Response: &llm.Response{Content: "hello"}},
			{Type: agent.EventFinal, Response: &llm.Response{Content: "hello"}},
		},
	}, HandlerConfig{})

	body, _ := json.Marshal(RunAgentInput{
		ThreadID: "thread-1",
		RunID:    "run-1",
		Messages: []InputMessage{{Role: "user", Content: "hi"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agent", bytes.NewReader(body))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"type":"RUN_STARTED"`) {
		t.Fatalf("missing RUN_STARTED in %q", out)
	}
	if !strings.Contains(out, `"type":"TEXT_MESSAGE_CONTENT"`) {
		t.Fatalf("missing TEXT_MESSAGE_CONTENT in %q", out)
	}
	if !strings.Contains(out, `"type":"RUN_FINISHED"`) {
		t.Fatalf("missing RUN_FINISHED in %q", out)
	}
}

func TestNewAgent_Info(t *testing.T) {
	a := NewAgent("http://example/agent", ClientConfig{Name: "remote", Description: "desc"})
	info := a.Info()
	if info.Name != "remote" || info.Description != "desc" {
		t.Fatalf("Info() = %+v", info)
	}
}

func TestRemoteAgent_ParseSSE(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"RUN_STARTED","threadId":"t1"}`,
		``,
		`data: {"type":"TEXT_MESSAGE_CONTENT","delta":"hi"}`,
		``,
		`data: {"type":"RUN_FINISHED","result":"hi"}`,
		``,
	}, "\n")

	a := &remoteAgent{}
	var types []agent.EventType
	if err := a.parseSSE(strings.NewReader(sse), func(ev agent.Event, err error) bool {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		types = append(types, ev.Type)
		return true
	}); err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	want := []agent.EventType{agent.EventToken, agent.EventFinal}
	if len(types) != len(want) {
		t.Fatalf("events = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event[%d] = %v, want %v", i, types[i], want[i])
		}
	}
	if a.threadID != "t1" {
		t.Fatalf("threadID = %q", a.threadID)
	}
}
