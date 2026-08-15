package agent

import (
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

// reqFromContinueOpts 会传 nil tools 指针;WithTools 不应 panic。
func TestApplyOptions_NilToolsPointer(t *testing.T) {
	req := &llm.Request{}
	opts := []Option{
		WithTools{Func("t", "desc", nil, nil)},
		WithToolChoice("auto"),
		WithModel("gpt-4o"),
	}
	applyOptions(req, nil, new(int), opts)
	if req.ToolChoice != "auto" {
		t.Errorf("ToolChoice = %q, want auto", req.ToolChoice)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", req.Model)
	}
}

func TestReqFromContinueOpts_WithToolsNoPanic(t *testing.T) {
	opts := []Option{
		WithTools{Func("t", "desc", nil, nil)},
		WithToolChoice("required"),
	}
	req := reqFromContinueOpts(opts)
	if req.ToolChoice != "required" {
		t.Errorf("ToolChoice = %q, want required", req.ToolChoice)
	}
}
