package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

// testClient 返回一个指向 srv 的 Client(静态凭据,避免依赖环境变量)。
func testClient(t *testing.T, srv *httptest.Server, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithStaticCredentials("AKIDEXAMPLE", "secret", ""),
	}
	return New("us-east-1", append(base, opts...)...)
}

// chunkFrame 把一段家族事件 JSON 包成 Bedrock 流式 chunk 帧:payload={"bytes":"<base64>"}。
func chunkFrame(innerJSON string) []byte {
	payload := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(innerJSON)) + `"}`)
	return encodeFrame(map[string]string{":event-type": "chunk", ":message-type": "event"}, payload)
}

func TestPickCodec(t *testing.T) {
	cases := []struct {
		id   string
		want string // codec.Name(),"" 表示 nil
	}{
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", "anthropic"},
		{"us.anthropic.claude-3-5-haiku-20241022-v1:0", "anthropic"}, // 跨区前缀
		{"eu.anthropic.claude-3-haiku-20240307-v1:0", "anthropic"},
		{"amazon.titan-text-express-v1", "titan"},
		{"amazon.titan-embed-text-v2:0", "titan"},
		{"meta.llama3-1-70b-instruct-v1:0", "llama"},
		{"us.meta.llama3-2-11b-instruct-v1:0", "llama"},
		{"mistral.mistral-large-2402-v1:0", "mistral"},
		{"mistral.mixtral-8x7b-instruct-v0:1", "mistral"},
		{"cohere.command-r-v1:0", ""}, // 未支持家族
		{"", ""},
	}
	for _, c := range cases {
		got := pickCodec(c.id)
		if c.want == "" {
			if got != nil {
				t.Errorf("pickCodec(%q) = %s, want nil", c.id, got.Name())
			}
			continue
		}
		if got == nil || got.Name() != c.want {
			t.Errorf("pickCodec(%q) = %v, want %s", c.id, got, c.want)
		}
	}
}

func TestModelPathEscape(t *testing.T) {
	// model id 里的 ':' 必须编码为 %3A(与 AWS SDK/SigV4 一致);'.' '-' 不编码。
	got := modelPathEscape("anthropic.claude-3-5-sonnet-20241022-v2:0")
	want := "anthropic.claude-3-5-sonnet-20241022-v2%3A0"
	if got != want {
		t.Fatalf("modelPathEscape = %q, want %q", got, want)
	}
}

func TestGenerate_Anthropic(t *testing.T) {
	var gotPath, gotAuth, gotAmzDate string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path // 解码后路径,含字面 ':'
		gotAuth = r.Header.Get("Authorization")
		gotAmzDate = r.Header.Get("X-Amz-Date")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{
			"model":"claude","stop_reason":"end_turn",
			"content":[{"type":"text","text":"Hello there"}],
			"usage":{"input_tokens":12,"output_tokens":4}
		}`)
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	resp, err := cli.Generate(context.Background(), llm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		System:   "be brief",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello there" || resp.StopReason != "end_turn" {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.Usage.InputTokens != 12 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	// 传输校验:签名头存在、路径含 model id 与 /invoke。
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 ") || gotAmzDate == "" {
		t.Fatalf("missing SigV4 headers: auth=%q date=%q", gotAuth, gotAmzDate)
	}
	if !strings.Contains(gotPath, "anthropic.claude-3-5-sonnet-20241022-v2:0") || !strings.HasSuffix(gotPath, "/invoke") {
		t.Fatalf("path = %q", gotPath)
	}
	// body 校验:Bedrock 版本字段存在,且**不**带 model。
	if gotBody["anthropic_version"] != "bedrock-2023-05-31" {
		t.Fatalf("anthropic_version = %v", gotBody["anthropic_version"])
	}
	if _, ok := gotBody["model"]; ok {
		t.Fatalf("body should not carry model field: %v", gotBody)
	}
	if gotBody["system"] != "be brief" {
		t.Fatalf("system = %v", gotBody["system"])
	}
}

func TestStream_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/invoke-with-response-stream") {
			t.Errorf("stream path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Write(chunkFrame(`{"type":"message_start","message":{"usage":{"input_tokens":9}}}`))
		w.Write(chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`))
		w.Write(chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`))
		w.Write(chunkFrame(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`))
		w.Write(chunkFrame(`{"type":"message_stop"}`))
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	var text strings.Builder
	var final llm.Chunk
	for c, err := range cli.Stream(context.Background(), llm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	}) {
		if err != nil {
			t.Fatalf("chunk err: %v", err)
		}
		text.WriteString(c.Delta)
		if c.Usage != nil || len(c.ToolCalls) > 0 {
			final = c
		}
	}
	if text.String() != "Hello" {
		t.Fatalf("streamed text = %q", text.String())
	}
	if final.Usage == nil || final.Usage.InputTokens != 9 || final.Usage.OutputTokens != 7 {
		t.Fatalf("final chunk = %#v usage=%#v", final, final.Usage)
	}
}

func TestStream_ToolCalls_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(chunkFrame(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"get_weather"}}`))
		w.Write(chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`))
		w.Write(chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"SF\"}"}}`))
		w.Write(chunkFrame(`{"type":"message_stop"}`))
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	var final llm.Chunk
	for c, err := range cli.Stream(context.Background(), llm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []llm.Message{{Role: llm.User, Content: "weather?"}},
	}) {
		if err != nil {
			t.Fatalf("chunk err: %v", err)
		}
		if len(c.ToolCalls) > 0 {
			final = c
		}
	}
	if len(final.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", final.ToolCalls)
	}
	tc := final.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Name != "get_weather" || string(tc.Arguments) != `{"city":"SF"}` {
		t.Fatalf("tool call = %#v args=%s", tc, tc.Arguments)
	}
}

func TestStream_ExceptionFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`))
		w.Write(encodeFrame(map[string]string{
			":message-type":   "exception",
			":exception-type": "throttlingException",
		}, []byte(`{"message":"slow down"}`)))
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	var gotErr error
	for _, err := range cli.Stream(context.Background(), llm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	}) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "throttlingException") {
		t.Fatalf("expected exception error, got %v", gotErr)
	}
}

func TestGenerate_Titan(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{
			"inputTextTokenCount":6,
			"results":[{"outputText":"  Bonjour  ","tokenCount":3,"completionReason":"FINISH"}]
		}`)
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	resp, err := cli.Generate(context.Background(), llm.Request{
		Model:    "amazon.titan-text-express-v1",
		Messages: []llm.Message{{Role: llm.User, Content: "translate hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Bonjour" || resp.StopReason != "FINISH" {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.Usage.InputTokens != 6 || resp.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	// Titan body 用 inputText + textGenerationConfig。
	if _, ok := gotBody["inputText"]; !ok {
		t.Fatalf("titan body missing inputText: %v", gotBody)
	}
}

func TestEmbed_Titan(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.Contains(r.URL.Path, "titan-embed") {
			t.Errorf("embed path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"embedding":[0.1,0.2,0.3]}`)
	}))
	defer srv.Close()

	cli := testClient(t, srv) // 默认 embedModel = amazon.titan-embed-text-v2:0
	vecs, err := cli.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 embed calls, got %d", calls)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 || vecs[1][2] != 0.3 {
		t.Fatalf("vecs = %#v", vecs)
	}
}

func TestEmbed_UnsupportedFamily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	// Llama 不实现 EmbedCodec → Embed 应报错(不发请求)。
	cli := testClient(t, srv, WithEmbedModel("meta.llama3-1-70b-instruct-v1:0"))
	if _, err := cli.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected error for family without embedding support")
	}
}

func TestGenerate_Llama(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"generation":" hi from llama ","prompt_token_count":8,"generation_token_count":5,"stop_reason":"stop"}`)
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	resp, err := cli.Generate(context.Background(), llm.Request{
		Model:    "meta.llama3-1-70b-instruct-v1:0",
		System:   "sys",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi from llama" || resp.StopReason != "stop" {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
	// Llama prompt 用官方 header 模板。
	prompt, _ := gotBody["prompt"].(string)
	if !strings.Contains(prompt, "<|begin_of_text|>") || !strings.Contains(prompt, "<|start_header_id|>assistant<|end_header_id|>") {
		t.Fatalf("llama prompt not templated: %q", prompt)
	}
}

func TestGenerate_Mistral(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"outputs":[{"text":" salut ","stop_reason":"stop"}]}`)
	}))
	defer srv.Close()

	cli := testClient(t, srv)
	resp, err := cli.Generate(context.Background(), llm.Request{
		Model:    "mistral.mistral-large-2402-v1:0",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "salut" || resp.StopReason != "stop" {
		t.Fatalf("resp = %#v", resp)
	}
	prompt, _ := gotBody["prompt"].(string)
	if !strings.Contains(prompt, "[INST]") || !strings.HasPrefix(prompt, "<s>") {
		t.Fatalf("mistral prompt not templated: %q", prompt)
	}
}

func TestGenerate_UnknownModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cli := testClient(t, srv)
	if _, err := cli.Generate(context.Background(), llm.Request{Model: "cohere.command-r-v1:0"}); err == nil {
		t.Fatal("expected error for unknown model family")
	}
}

func TestGenerate_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"message":"bad request"}`)
	}))
	defer srv.Close()
	cli := testClient(t, srv)
	_, err := cli.Generate(context.Background(), llm.Request{
		Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("expected API error, got %v", err)
	}
}
