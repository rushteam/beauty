package instruct

import (
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func req(system string, msgs ...llm.Message) llm.Request {
	return llm.Request{System: system, Messages: msgs}
}

func msg(role llm.Role, content string) llm.Message {
	return llm.Message{Role: role, Content: content}
}

// ---- ChatML ----

func TestChatML_Basic(t *testing.T) {
	got := ChatML.Format(req("你是助手",
		msg(llm.User, "你好"),
		msg(llm.Assistant, "你好!"),
		msg(llm.User, "天气?"),
	))
	want := "<|im_start|>system\n你是助手<|im_end|>\n" +
		"<|im_start|>user\n你好<|im_end|>\n" +
		"<|im_start|>assistant\n你好!<|im_end|>\n" +
		"<|im_start|>user\n天气?<|im_end|>\n" +
		"<|im_start|>assistant\n"
	if got != want {
		t.Errorf("ChatML:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestChatML_NoSystem(t *testing.T) {
	got := ChatML.Format(req("", msg(llm.User, "hi")))
	if strings.HasPrefix(got, "<|im_start|>system") {
		t.Error("should not have system block when System is empty")
	}
	if !strings.HasPrefix(got, "<|im_start|>user\nhi") {
		t.Errorf("unexpected: %q", got)
	}
}

// ---- Llama3 ----

func TestLlama3_Basic(t *testing.T) {
	got := Llama3.Format(req("System prompt",
		msg(llm.User, "Hello"),
	))
	if !strings.HasPrefix(got, "<|begin_of_text|>") {
		t.Error("missing BOS")
	}
	if !strings.Contains(got, "<|start_header_id|>system<|end_header_id|>\n\nSystem prompt<|eot_id|>") {
		t.Error("missing system block")
	}
	if !strings.Contains(got, "<|start_header_id|>user<|end_header_id|>\n\nHello<|eot_id|>") {
		t.Error("missing user block")
	}
	if !strings.HasSuffix(got, "<|start_header_id|>assistant<|end_header_id|>\n\n") {
		t.Error("missing trailing assistant prefix")
	}
}

// ---- Mistral ----

func TestMistral_Basic(t *testing.T) {
	got := Mistral.Format(req("Be helpful",
		msg(llm.User, "Hello"),
		msg(llm.Assistant, "Hi!"),
		msg(llm.User, "Bye"),
	))
	if !strings.HasPrefix(got, "<s>") {
		t.Error("missing BOS")
	}
	if !strings.Contains(got, "[INST] Be helpful") {
		t.Error("missing system")
	}
	if !strings.Contains(got, "[INST] Hello [/INST]") {
		t.Error("missing user turn")
	}
	if !strings.Contains(got, "Hi!</s>") {
		t.Error("missing assistant turn")
	}
}

// ---- Alpaca ----

func TestAlpaca_Basic(t *testing.T) {
	got := Alpaca.Format(req("You are helpful",
		msg(llm.User, "Explain X"),
	))
	if !strings.Contains(got, "### System:\nYou are helpful\n\n") {
		t.Error("missing system block")
	}
	if !strings.Contains(got, "### Instruction:\nExplain X\n\n") {
		t.Error("missing instruction block")
	}
	if !strings.HasSuffix(got, "### Response:\n") {
		t.Error("missing trailing response prefix")
	}
}

// ---- Gemma ----

func TestGemma_Basic(t *testing.T) {
	got := Gemma.Format(req("System info",
		msg(llm.User, "Hello"),
	))
	if !strings.HasPrefix(got, "<bos>") {
		t.Error("missing BOS")
	}
	if !strings.Contains(got, "[System: System info]") {
		t.Error("system should be wrapped in [System: ...]")
	}
	if !strings.Contains(got, "<start_of_turn>user\nHello<end_of_turn>") {
		t.Error("missing user turn")
	}
	if !strings.HasSuffix(got, "<start_of_turn>model\n") {
		t.Error("missing trailing model prefix")
	}
}

// ---- 通用行为 ----

func TestFormat_EmptyRequest(t *testing.T) {
	got := ChatML.Format(llm.Request{})
	if got != "<|im_start|>assistant\n" {
		t.Errorf("empty request should produce only trailing assistant prefix, got %q", got)
	}
}

func TestFormat_SystemMessageInMessages(t *testing.T) {
	got := ChatML.Format(req("",
		msg(llm.System, "内嵌system"),
		msg(llm.User, "hi"),
	))
	if !strings.Contains(got, "<|im_start|>system\n内嵌system<|im_end|>") {
		t.Error("should handle system role in messages")
	}
}

func TestFormat_ToolMessagesSkipped(t *testing.T) {
	got := ChatML.Format(req("sys",
		msg(llm.User, "call tool"),
		llm.Message{Role: llm.Tool, Content: "tool result", ToolCallID: "tc1"},
		msg(llm.User, "next"),
	))
	if strings.Contains(got, "tool result") {
		t.Error("tool messages should be skipped")
	}
	if !strings.Contains(got, "call tool") || !strings.Contains(got, "next") {
		t.Error("non-tool messages should be preserved")
	}
}

func TestFormat_MultimodalFallback(t *testing.T) {
	got := ChatML.Format(llm.Request{
		Messages: []llm.Message{{
			Role: llm.User,
			Parts: []llm.Part{
				{Type: llm.PartText, Text: "看这张图"},
				{Type: llm.PartImage, ImageURL: "http://example.com/img.jpg"},
				{Type: llm.PartText, Text: "是什么?"},
			},
		}},
	})
	if !strings.Contains(got, "看这张图\n是什么?") {
		t.Errorf("multimodal should extract text parts, got %q", got)
	}
}

func TestFormat_MultiTurn(t *testing.T) {
	got := Llama3.Format(req("sys",
		msg(llm.User, "q1"),
		msg(llm.Assistant, "a1"),
		msg(llm.User, "q2"),
		msg(llm.Assistant, "a2"),
		msg(llm.User, "q3"),
	))
	if strings.Count(got, "<|start_header_id|>user") != 3 {
		t.Error("should have 3 user turns")
	}
	if strings.Count(got, "<|start_header_id|>assistant") != 3 { // 2 in history + 1 trailing
		t.Error("should have 3 assistant headers (2 in history + 1 trailing)")
	}
}

// ---- MergeStops ----

func TestMergeStops_BothNonEmpty(t *testing.T) {
	got := ChatML.MergeStops([]string{"custom"})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != "custom" || got[1] != "<|im_end|>" {
		t.Errorf("got %v", got)
	}
}

func TestMergeStops_UserEmpty(t *testing.T) {
	got := ChatML.MergeStops(nil)
	if len(got) != 1 || got[0] != "<|im_end|>" {
		t.Errorf("got %v", got)
	}
}

func TestMergeStops_TemplateEmpty(t *testing.T) {
	tmpl := Template{Name: "bare"}
	got := tmpl.MergeStops([]string{"a"})
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("got %v", got)
	}
}

func TestMergeStops_Dedup(t *testing.T) {
	got := ChatML.MergeStops([]string{"<|im_end|>", "other"})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (deduped)", len(got))
	}
}

// ---- 所有内置模板名称 ----

func TestBuiltinNames(t *testing.T) {
	for _, tmpl := range []*Template{&ChatML, &Llama3, &Mistral, &Alpaca, &Gemma} {
		if tmpl.Name == "" {
			t.Error("built-in template has empty Name")
		}
		if len(tmpl.StopStrings) == 0 {
			t.Errorf("%s: missing StopStrings", tmpl.Name)
		}
	}
}
