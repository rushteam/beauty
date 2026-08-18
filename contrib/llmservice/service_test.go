package llmservice

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type stubClient struct{ content string }

func (c stubClient) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.content}, nil
}
func (c stubClient) Stream(_ context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		yield(llm.Chunk{Delta: c.content}, nil)
	}
}

func TestAgentService_StartStop(t *testing.T) {
	runner := &agent.Runner{
		Name:   "test",
		Client: stubClient{content: "hello"},
	}
	svc := New("test", runner, Workers(2))

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = svc.Start(ctx)
	}()

	// 等待就绪
	select {
	case <-svc.Ready():
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ready")
	}

	// 提交任务
	var got atomic.Value
	err := svc.Submit(ctx, Task{
		Request: llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "hi"}}},
		Callback: func(o agent.RunOutcome) {
			got.Store(o)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 等结果
	time.Sleep(200 * time.Millisecond)
	cancel()
	wg.Wait()

	v := got.Load()
	if v == nil {
		t.Fatal("callback not called")
	}
	outcome := v.(agent.RunOutcome)
	if outcome.Status != agent.StatusDone {
		t.Fatalf("expected done, got %s: %v", outcome.Status, outcome.Err)
	}
	if outcome.Response == nil || outcome.Response.Content != "hello" {
		t.Fatalf("unexpected response: %+v", outcome.Response)
	}
}

func TestAgentService_Handler(t *testing.T) {
	runner := &agent.Runner{
		Name:   "test-http",
		Client: stubClient{content: "streamed"},
	}
	svc := New("test-http", runner, Workers(1))

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = svc.Start(ctx)
	}()
	<-svc.Ready()

	h := svc.Handler()
	body := `{"messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "streamed") {
		t.Fatalf("expected stream output, got: %s", w.Body.String())
	}

	cancel()
	wg.Wait()
}

func TestTaskFromMessage(t *testing.T) {
	original := Task{
		Request: llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "msg"}}},
	}
	data, _ := json.Marshal(original)
	got, err := TaskFromMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Request.Messages) != 1 || got.Request.Messages[0].Content != "msg" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestWithAgent_Integration(t *testing.T) {
	runner := &agent.Runner{
		Name:   "svc-agent",
		Client: stubClient{content: "integrated"},
	}
	opt := WithAgent("svc-agent", runner, Workers(1))
	if opt == nil {
		t.Fatal("WithAgent returned nil")
	}
}
