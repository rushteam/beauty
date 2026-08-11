package anthropicwire

import (
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func TestBuildMessages_ToolRoundTrip(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.User, Content: "hi"},
		{Role: llm.Assistant, Content: "let me check", ToolCalls: []llm.ToolCall{
			{ID: "t1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)},
		}},
		{Role: llm.Tool, ToolCallID: "t1", Content: "sunny"},
		{Role: llm.Tool, ToolCallID: "t2", Content: "warm"},
	}
	got := BuildMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("want 3 messages (user, assistant, merged-tool user), got %d", len(got))
	}

	// assistant 回合应转成 text + tool_use 块
	asBlocks, ok := got[1].Content.([]Block)
	if !ok || len(asBlocks) != 2 {
		t.Fatalf("assistant content not 2 blocks: %#v", got[1].Content)
	}
	if asBlocks[0].Type != "text" || asBlocks[1].Type != "tool_use" || asBlocks[1].Name != "get_weather" {
		t.Fatalf("unexpected assistant blocks: %#v", asBlocks)
	}

	// 相邻两条 tool 结果并入一个 user 回合(两个 tool_result 块)
	if got[2].Role != "user" {
		t.Fatalf("merged tool results should be a user turn, got role %q", got[2].Role)
	}
	trBlocks, ok := got[2].Content.([]Block)
	if !ok || len(trBlocks) != 2 {
		t.Fatalf("tool results not merged into 2 blocks: %#v", got[2].Content)
	}
	if trBlocks[0].ToolUseID != "t1" || trBlocks[1].ToolUseID != "t2" {
		t.Fatalf("tool_result ids wrong: %#v", trBlocks)
	}
}

func TestBuildMessages_EmptyToolArgs(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "t1", Name: "ping"}}},
	}
	blocks := BuildMessages(msgs)[0].Content.([]Block)
	if string(blocks[0].Input) != "{}" {
		t.Fatalf("empty tool args should default to {}, got %s", blocks[0].Input)
	}
}

func TestBuildParts_Multimodal(t *testing.T) {
	parts := []llm.Part{
		{Type: llm.PartText, Text: "look"},
		{Type: llm.PartImage, ImageURL: "data:image/png;base64,QUJD"},
		{Type: llm.PartImage, ImageURL: "https://example.com/x.png"},
	}
	blocks := BuildParts(parts)
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d", len(blocks))
	}
	if blocks[1].Source == nil || blocks[1].Source.Type != "base64" || blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "QUJD" {
		t.Fatalf("base64 image block wrong: %#v", blocks[1].Source)
	}
	if blocks[2].Source == nil || blocks[2].Source.Type != "url" || blocks[2].Source.URL != "https://example.com/x.png" {
		t.Fatalf("url image block wrong: %#v", blocks[2].Source)
	}
}

func TestBuildTools_DefaultSchema(t *testing.T) {
	ts := BuildTools([]llm.ToolDef{{Name: "noargs"}})
	if len(ts) != 1 || string(ts[0].InputSchema) != `{"type":"object","properties":{}}` {
		t.Fatalf("missing default input_schema: %#v", ts)
	}
	if BuildTools(nil) != nil {
		t.Fatalf("nil defs should give nil tools")
	}
}

func TestBuildToolChoice(t *testing.T) {
	cases := map[string]any{
		"":         nil,
		"auto":     map[string]string{"type": "auto"},
		"none":     map[string]string{"type": "none"},
		"required": map[string]string{"type": "any"},
		"myTool":   map[string]string{"type": "tool", "name": "myTool"},
	}
	for in, want := range cases {
		got := BuildToolChoice(in)
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if string(gb) != string(wb) {
			t.Fatalf("BuildToolChoice(%q)=%s want %s", in, gb, wb)
		}
	}
}

func TestResolveMaxTokens(t *testing.T) {
	if ResolveMaxTokens(0) != DefaultMaxTokens || ResolveMaxTokens(-5) != DefaultMaxTokens {
		t.Fatalf("non-positive should default")
	}
	if ResolveMaxTokens(42) != 42 {
		t.Fatalf("positive should pass through")
	}
}

func TestParseResponse(t *testing.T) {
	body := `{
		"model": "claude-x",
		"stop_reason": "tool_use",
		"content": [
			{"type":"text","text":"hello "},
			{"type":"text","text":"world"},
			{"type":"tool_use","id":"t1","name":"foo","input":{"a":1}}
		],
		"usage": {"input_tokens": 11, "output_tokens": 7}
	}`
	r, err := ParseResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != "hello world" {
		t.Fatalf("content concat wrong: %q", r.Content)
	}
	if r.Model != "claude-x" || r.StopReason != "tool_use" {
		t.Fatalf("meta wrong: %#v", r)
	}
	if r.Usage.InputTokens != 11 || r.Usage.OutputTokens != 7 {
		t.Fatalf("usage wrong: %#v", r.Usage)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "foo" || string(r.ToolCalls[0].Arguments) != `{"a":1}` {
		t.Fatalf("tool call wrong: %#v", r.ToolCalls)
	}
}

func TestEventAccumulator_TextAndTools(t *testing.T) {
	events := []string{
		`{"type":"message_start","usage":{"input_tokens":5}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"He"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"llo"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"foo"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"1}"}}`,
		`{"type":"message_delta","usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}
	acc := NewEventAccumulator()
	var text string
	var done bool
	for _, e := range events {
		d, stop := acc.Feed([]byte(e))
		text += d
		if stop {
			done = true
		}
	}
	if !done {
		t.Fatal("expected message_stop to signal done")
	}
	if text != "Hello" {
		t.Fatalf("text delta assembly wrong: %q", text)
	}
	tcs, usage := acc.Result()
	if len(tcs) != 1 || tcs[0].ID != "t1" || tcs[0].Name != "foo" || string(tcs[0].Arguments) != `{"a":1}` {
		t.Fatalf("streamed tool_call wrong: %#v", tcs)
	}
	if usage.InputTokens != 5 || usage.OutputTokens != 9 {
		t.Fatalf("usage wrong: %#v", usage)
	}
}

func TestEventAccumulator_EmptyToolArgs(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Feed([]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"ping"}}`))
	acc.Feed([]byte(`{"type":"message_stop"}`))
	tcs, _ := acc.Result()
	if len(tcs) != 1 || string(tcs[0].Arguments) != "{}" {
		t.Fatalf("empty streamed tool args should be {}: %#v", tcs)
	}
}

func TestEventAccumulator_BadJSONSkipped(t *testing.T) {
	acc := NewEventAccumulator()
	if d, done := acc.Feed([]byte("not json")); d != "" || done {
		t.Fatalf("bad json should be skipped, got delta=%q done=%v", d, done)
	}
}

func TestBuildMessages_CacheControl(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.User, Content: "cached prompt", CacheControl: "ephemeral"},
		{Role: llm.User, Content: "normal"},
	}
	got := BuildMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("want 2 messages, got %d", len(got))
	}
	blocks, ok := got[0].Content.([]Block)
	if !ok || len(blocks) != 1 {
		t.Fatalf("cached message should have 1 block, got %#v", got[0].Content)
	}
	if blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache_control not set: %#v", blocks[0])
	}
	if _, ok := got[1].Content.(string); !ok {
		t.Fatalf("non-cached message should stay as string, got %T", got[1].Content)
	}
}

func TestBuildSystem_WithCacheControl(t *testing.T) {
	s := BuildSystem("hello", "ephemeral")
	blocks, ok := s.([]Block)
	if !ok || len(blocks) != 1 {
		t.Fatalf("cached system should be block array, got %T", s)
	}
	if blocks[0].Text != "hello" || blocks[0].CacheControl == nil || blocks[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("block wrong: %#v", blocks[0])
	}

	plain := BuildSystem("hello", "")
	if str, ok := plain.(string); !ok || str != "hello" {
		t.Fatalf("non-cached system should be string, got %T %v", plain, plain)
	}

	if BuildSystem("", "ephemeral") != nil {
		t.Fatal("empty system should return nil")
	}
}

func TestParseResponse_Thinking(t *testing.T) {
	body := `{
		"model": "claude-x",
		"stop_reason": "end_turn",
		"content": [
			{"type":"thinking","thinking":"let me think..."},
			{"type":"text","text":"the answer"}
		],
		"usage": {"input_tokens": 10, "output_tokens": 20, "cache_creation_input_tokens": 5, "cache_read_input_tokens": 3}
	}`
	r, err := ParseResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != "the answer" {
		t.Fatalf("content wrong: %q", r.Content)
	}
	if r.Thinking != "let me think..." {
		t.Fatalf("thinking wrong: %q", r.Thinking)
	}
	if r.Usage.CacheCreationInputTokens != 5 || r.Usage.CacheReadInputTokens != 3 {
		t.Fatalf("cache usage wrong: %+v", r.Usage)
	}
}

func TestEventAccumulator_ThinkingStream(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":8}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"step 1"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" step 2"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"result"}}`,
		`{"type":"message_delta","usage":{"output_tokens":15}}`,
		`{"type":"message_stop"}`,
	}
	acc := NewEventAccumulator()
	var text, thinking string
	var done bool
	for _, e := range events {
		sd := acc.FeedExt([]byte(e))
		text += sd.Text
		thinking += sd.Thinking
		if sd.Done {
			done = true
		}
	}
	if !done {
		t.Fatal("expected done")
	}
	if text != "result" {
		t.Fatalf("text wrong: %q", text)
	}
	if thinking != "step 1 step 2" {
		t.Fatalf("thinking wrong: %q", thinking)
	}
	if acc.ThinkingText() != "step 1 step 2" {
		t.Fatalf("ThinkingText() wrong: %q", acc.ThinkingText())
	}
	_, usage := acc.Result()
	if usage.InputTokens != 10 || usage.OutputTokens != 15 || usage.CacheReadInputTokens != 8 {
		t.Fatalf("usage wrong: %+v", usage)
	}
}
