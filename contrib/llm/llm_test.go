package llm_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/anthropic"
	"github.com/rushteam/beauty/contrib/llm/openai"
)

func isStream(r *http.Request) bool {
	b, _ := io.ReadAll(r.Body)
	return bytes.Contains(b, []byte(`"stream":true`))
}

func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, l := range lines {
		_, _ = io.WriteString(w, l+"\n\n")
	}
}

// ---- OpenAI ----

func openaiMock(t *testing.T) *openai.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/embeddings"):
			_, _ = io.WriteString(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
		case isStream(r):
			sse(w,
				`data: {"choices":[{"delta":{"content":"he"}}]}`,
				`data: {"choices":[{"delta":{"content":"llo"}}]}`,
				`data: [DONE]`,
			)
		default:
			_, _ = io.WriteString(w, `{"model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return openai.New("test-key", openai.WithBaseURL(srv.URL))
}

func TestOpenAI_Generate(t *testing.T) {
	c := openaiMock(t)
	resp, err := c.Generate(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "hi there" || resp.StopReason != "stop" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestOpenAI_Stream(t *testing.T) {
	c := openaiMock(t)
	ch, err := c.Stream(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	got, done := collect(t, ch)
	if got != "hello" {
		t.Fatalf("stream text = %q, want hello", got)
	}
	if !done {
		t.Fatal("应收到 Done")
	}
}

func TestOpenAI_Embed(t *testing.T) {
	c := openaiMock(t)
	vecs, err := c.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("embeddings = %v", vecs)
	}
}

// ---- Anthropic ----

func TestAnthropic_GenerateAndStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("缺 anthropic 认证头")
		}
		if isStream(r) {
			sse(w,
				`data: {"type":"content_block_delta","delta":{"text":"foo"}}`,
				`data: {"type":"content_block_delta","delta":{"text":"bar"}}`,
				`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
				`data: {"type":"message_stop"}`,
			)
			return
		}
		_, _ = io.WriteString(w, `{"model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":4,"output_tokens":1}}`)
	}))
	defer srv.Close()
	c := anthropic.New("k", anthropic.WithBaseURL(srv.URL))

	resp, err := c.Generate(context.Background(), llm.Request{Model: "claude-x", System: "be brief", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "hi" || resp.Usage.InputTokens != 4 {
		t.Fatalf("resp = %+v", resp)
	}

	ch, err := c.Stream(context.Background(), llm.Request{Model: "claude-x", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	got, done := collect(t, ch)
	if got != "foobar" || !done {
		t.Fatalf("stream = %q done=%v", got, done)
	}
}

// ---- 中间件 ----

func TestFallback(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	primary := openai.New("k", openai.WithBaseURL(bad.URL)) // 总是 500
	backup := openaiMock(t)                                 // 正常

	c := llm.Fallback(primary, backup)
	resp, err := c.Generate(context.Background(), llm.Request{Model: "x", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("fallback 应切到备用: %v", err)
	}
	if resp.Content != "hi there" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestMetered(t *testing.T) {
	var gotUsage llm.Usage
	var gotModel string
	c := llm.Metered(openaiMock(t), func(_ context.Context, model string, u llm.Usage, _ time.Duration) {
		gotModel, gotUsage = model, u
	})
	_, err := c.Generate(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gotModel != "gpt-4o" || gotUsage.OutputTokens != 2 {
		t.Fatalf("计量回调 model=%q usage=%+v", gotModel, gotUsage)
	}
}

func collect(t *testing.T, ch <-chan llm.Chunk) (text string, done bool) {
	t.Helper()
	var sb strings.Builder
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("stream chunk err: %v", c.Err)
		}
		sb.WriteString(c.Delta)
		if c.Done {
			done = true
		}
	}
	return sb.String(), done
}
