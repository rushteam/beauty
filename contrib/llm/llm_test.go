package llm_test

import (
	"bytes"
	"context"
	"io"
	"iter"
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
	got, hasUsage := collect(t, c.Stream(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}))
	if got != "hello" {
		t.Fatalf("stream text = %q, want hello", got)
	}
	if !hasUsage {
		t.Fatal("应收到 Usage")
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

func TestOpenAI_Azure(t *testing.T) {
	var gotFullPath, gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFullPath = r.URL.Path + "?" + r.URL.RawQuery
		gotAPIKey = r.Header.Get("api-key")
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"model":"gpt-4o","choices":[{"message":{"content":"az"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer srv.Close()

	c := openai.NewAzure(srv.URL, "my-deploy", "2024-10-21", "azkey")
	resp, err := c.Generate(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err != nil {
		t.Fatalf("azure generate: %v", err)
	}
	if resp.Content != "az" {
		t.Fatalf("content = %q", resp.Content)
	}
	if gotAPIKey != "azkey" || gotAuth != "" {
		t.Fatalf("Azure 应用 api-key 头(得 %q),不用 Authorization(得 %q)", gotAPIKey, gotAuth)
	}
	if gotFullPath != "/openai/deployments/my-deploy/chat/completions?api-version=2024-10-21" {
		t.Fatalf("Azure URL = %q", gotFullPath)
	}
}

// 兼容厂商:换 BaseURL 即用同一 openai provider(此处以 Kimi 的 BaseURL 常量为例走打桩)。
func TestOpenAI_CompatibleBaseURL(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"model":"moonshot-v1","choices":[{"message":{"content":"ok"}}],"usage":{}}`)
	}))
	defer srv.Close()
	// 生产时用 openai.BaseURLMoonshot / BaseURLZhipu / ... ;测试用打桩地址,验证是同一条 Bearer 认证路径。
	c := openai.New("kimikey", openai.WithBaseURL(srv.URL))
	if _, err := c.Generate(context.Background(), llm.Request{Model: "moonshot-v1", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gotAuth != "Bearer kimikey" {
		t.Fatalf("兼容厂商应走 Bearer 认证, got %q", gotAuth)
	}
}

func TestOpenAI_GenerateImage(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"data":[{"url":"https://example.com/a.png","revised_prompt":"a cat"}]}`)
	}))
	defer srv.Close()

	c := openai.New("k", openai.WithBaseURL(srv.URL))
	resp, err := c.GenerateImage(context.Background(), llm.ImageRequest{
		Model: "dall-e-3", Prompt: "a cat", Size: "1024x1024",
	})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/images/generations") {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"prompt":"a cat"`) || !strings.Contains(gotBody, `"model":"dall-e-3"`) {
		t.Fatalf("body = %s", gotBody)
	}
	if len(resp.Data) != 1 || resp.Data[0].URL != "https://example.com/a.png" || resp.Data[0].RevisedPrompt != "a cat" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestOpenAI_EditImage(t *testing.T) {
	var gotPath, gotCT string
	var gotPrompt, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			http.Error(w, err.Error(), 400)
			return
		}
		gotPrompt = r.FormValue("prompt")
		gotModel = r.FormValue("model")
		f, _, err := r.FormFile("image")
		if err != nil {
			t.Errorf("FormFile image: %v", err)
		} else {
			b, _ := io.ReadAll(f)
			if string(b) != "PNGDATA" {
				t.Errorf("image bytes = %q", b)
			}
			_ = f.Close()
		}
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"YWJj"}]}`)
	}))
	defer srv.Close()

	c := openai.New("k", openai.WithBaseURL(srv.URL))
	resp, err := c.EditImage(context.Background(), llm.ImageEditRequest{
		Model: "dall-e-2", Prompt: "add hat",
		Image: strings.NewReader("PNGDATA"), ResponseFormat: "b64_json",
	})
	if err != nil {
		t.Fatalf("EditImage: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/images/edits") {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	if gotPrompt != "add hat" || gotModel != "dall-e-2" {
		t.Fatalf("prompt=%q model=%q", gotPrompt, gotModel)
	}
	if len(resp.Data) != 1 || resp.Data[0].B64JSON != "YWJj" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestOpenAI_Speech(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("FAKEMP3"))
	}))
	defer srv.Close()

	c := openai.New("k", openai.WithBaseURL(srv.URL))
	audio, err := c.Speech(context.Background(), llm.SpeechRequest{
		Model: "tts-1", Input: "hello", Voice: "alloy", Speed: 1.0,
	})
	if err != nil {
		t.Fatalf("Speech: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/audio/speech") {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"input":"hello"`) || !strings.Contains(gotBody, `"voice":"alloy"`) {
		t.Fatalf("body = %s", gotBody)
	}
	if string(audio) != "FAKEMP3" {
		t.Fatalf("audio = %q", audio)
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

	got, hasUsage := collect(t, c.Stream(context.Background(), llm.Request{Model: "claude-x", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}))
	if got != "foobar" || !hasUsage {
		t.Fatalf("stream = %q hasUsage=%v", got, hasUsage)
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

func collect(t *testing.T, seq iter.Seq2[llm.Chunk, error]) (text string, hasUsage bool) {
	t.Helper()
	var sb strings.Builder
	for c, err := range seq {
		if err != nil {
			t.Fatalf("stream err: %v", err)
		}
		sb.WriteString(c.Delta)
		if c.Usage != nil {
			hasUsage = true
		}
	}
	return sb.String(), hasUsage
}

// ---- Cache ----

func TestCache_Generate(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"cached"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	cli := openai.New("k", openai.WithBaseURL(srv.URL))
	cc := llm.Cache(cli, llm.NewMemoryCacheStore(100))

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	r1, err := cc.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := cc.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Content != "cached" || r2.Content != "cached" {
		t.Fatalf("r1=%q r2=%q", r1.Content, r2.Content)
	}
	if calls != 1 {
		t.Fatalf("should call API once, got %d", calls)
	}
	st := cc.Stats()
	if st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

func TestCache_SkipNonZeroTemp(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()
	cli := openai.New("k", openai.WithBaseURL(srv.URL))
	cc := llm.Cache(cli, llm.NewMemoryCacheStore(100))

	req := llm.Request{Model: "m", Temperature: 0.7, Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	_, _ = cc.Generate(context.Background(), req)
	_, _ = cc.Generate(context.Background(), req)
	if calls != 2 {
		t.Fatalf("non-zero temp should not cache, calls=%d", calls)
	}
}

func TestCache_Stream(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		sse(w,
			`data: {"choices":[{"delta":{"content":"ab"}}]}`,
			`data: {"choices":[{"delta":{"content":"cd"}}]}`,
			`data: [DONE]`,
		)
	}))
	defer srv.Close()
	cli := openai.New("k", openai.WithBaseURL(srv.URL))
	cc := llm.Cache(cli, llm.NewMemoryCacheStore(100))

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	got, _ := collect(t, cc.Stream(context.Background(), req))
	if got != "abcd" {
		t.Fatalf("first stream=%q", got)
	}
	// second call should be cached
	got2, _ := collect(t, cc.Stream(context.Background(), req))
	if got2 != "abcd" {
		t.Fatalf("cached stream=%q", got2)
	}
	if calls != 1 {
		t.Fatalf("API called %d times", calls)
	}
}

// ---- Budget ----

func TestBudget(t *testing.T) {
	cli := openaiMock(t)
	bc := llm.Budget(cli, 10) // budget of 10 tokens

	req := llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	_, err := bc.Generate(context.Background(), req) // uses 3+2=5 tokens
	if err != nil {
		t.Fatal(err)
	}
	if bc.Used() != 5 {
		t.Fatalf("used=%d", bc.Used())
	}
	if bc.Remaining() != 5 {
		t.Fatalf("remaining=%d", bc.Remaining())
	}
	_, err = bc.Generate(context.Background(), req) // uses another 5 → 10 total
	if err != nil {
		t.Fatal(err)
	}
	_, err = bc.Generate(context.Background(), req) // should be blocked
	if err != llm.ErrBudgetExceeded {
		t.Fatalf("expected budget exceeded, got %v", err)
	}
	bc.Reset()
	if bc.Used() != 0 {
		t.Fatal("reset failed")
	}
}

// ---- Output Guard ----

func TestOutputGuard_Generate(t *testing.T) {
	cli := openaiMock(t)
	safe := llm.GuardOutput(cli, llm.MaxOutputLen(3))

	_, err := safe.Generate(context.Background(), llm.Request{Model: "x", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if err == nil {
		t.Fatal("should block: 'hi there' > 3 runes")
	}
	var ge *llm.GuardError
	if !strings.Contains(err.Error(), "max_output_len") {
		t.Fatalf("err=%v, want GuardError", err)
	}
	_ = ge
}

func TestOutputGuard_Stream(t *testing.T) {
	cli := openaiMock(t)
	safe := llm.GuardOutput(cli, llm.MaxOutputLen(3))

	var gotErr error
	for _, err := range safe.Stream(context.Background(), llm.Request{Model: "x", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "max_output_len") {
		t.Fatalf("stream should get output guard error, got %v", gotErr)
	}
}
