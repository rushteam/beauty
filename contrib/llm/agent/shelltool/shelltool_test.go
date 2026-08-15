package shelltool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestNewPolicy(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{
		AllowList: []string{`^ls\b`, `^echo\b`},
		DenyList:  []string{`rm\s+-rf`, `sudo\b`},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	if len(p.allows) != 2 || len(p.denies) != 2 {
		t.Fatalf("allows=%d denies=%d, want 2/2", len(p.allows), len(p.denies))
	}
}

func TestPolicyEvaluate_AllowedByAllowList(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{
		AllowList: []string{`^echo\b`},
		DenyList:  []string{`rm\s`},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	ok, reason := p.Evaluate("echo hello")
	if !ok {
		t.Fatalf("want allowed, got denied: %s", reason)
	}
	if !strings.Contains(reason, "allow") {
		t.Fatalf("reason = %q, want allow match mention", reason)
	}
}

func TestPolicyEvaluate_DeniedByDenyList(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{
		AllowList: []string{`^echo\b`},
		DenyList:  []string{`rm\s+-rf`},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	ok, reason := p.Evaluate("rm -rf /")
	if ok {
		t.Fatalf("want denied, reason = %q", reason)
	}
	if !strings.Contains(reason, "deny") {
		t.Fatalf("reason = %q, want deny match mention", reason)
	}
}

func TestPolicyEvaluate_AllowListTakesPrecedenceOverDenyList(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{
		AllowList: []string{`echo.*rm`},
		DenyList:  []string{`rm`},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	ok, _ := p.Evaluate("echo rm -rf")
	if !ok {
		t.Fatal("allow list should take precedence over deny list")
	}
}

func TestPolicyEvaluate_DefaultAllow(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{
		DenyList: []string{`^sudo\b`},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	ok, reason := p.Evaluate("pwd")
	if !ok {
		t.Fatalf("want default allow, reason = %q", reason)
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

func TestPolicyEvaluate_EmptyCommandDenied(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	ok, reason := p.Evaluate("   ")
	if ok {
		t.Fatal("empty command should be denied")
	}
	if reason != "empty command" {
		t.Fatalf("reason = %q, want empty command", reason)
	}
}

func TestNewPolicy_InvalidRegex(t *testing.T) {
	_, err := NewPolicy(PolicyConfig{AllowList: []string{"["}})
	if err == nil {
		t.Fatal("expected error for invalid allow regex")
	}
	if !strings.Contains(err.Error(), "allow pattern") {
		t.Fatalf("err = %v", err)
	}

	_, err = NewPolicy(PolicyConfig{DenyList: []string{"("}})
	if err == nil {
		t.Fatal("expected error for invalid deny regex")
	}
	if !strings.Contains(err.Error(), "deny pattern") {
		t.Fatalf("err = %v", err)
	}
}

func TestTruncateHeadTail(t *testing.T) {
	const maxBytes = 40
	data := []byte(strings.Repeat("a", 100))
	truncated, out := truncateHeadTail(data, maxBytes)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if len(out) > maxBytes {
		t.Fatalf("output len = %d, want <= %d", len(out), maxBytes)
	}
	if !strings.HasPrefix(out, "a") {
		t.Fatalf("head not preserved: %q", out)
	}
	if !strings.HasSuffix(out, "a") {
		t.Fatalf("tail not preserved: %q", out)
	}
	if !strings.Contains(out, "[... truncated 60 bytes ...]") {
		t.Fatalf("missing marker: %q", out)
	}
}

func TestTruncateHeadTail_NoTruncationWhenUnderLimit(t *testing.T) {
	data := []byte("hello")
	truncated, out := truncateHeadTail(data, 100)
	if truncated {
		t.Fatal("should not truncate")
	}
	if out != "hello" {
		t.Fatalf("out = %q", out)
	}
}

func TestNew_ToolDefinition(t *testing.T) {
	tool := New(Config{})

	if tool.Def.Name != "shell" {
		t.Fatalf("name = %q, want shell", tool.Def.Name)
	}
	if tool.Def.Description == "" {
		t.Fatal("description should not be empty")
	}
	if tool.Permission != agent.PermitAsk {
		t.Fatalf("Permission = %v, want PermitAsk", tool.Permission)
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.Def.Parameters, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T", schema["properties"])
	}
	cmd, ok := props["command"].(map[string]any)
	if !ok || cmd["type"] != "string" {
		t.Fatalf("command property = %v", props["command"])
	}
}

func TestShellTool_PolicyDeny(t *testing.T) {
	p, err := NewPolicy(PolicyConfig{DenyList: []string{`^rm\b`}})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	tool := newShellTool(Config{Policy: p}, mockExecutor([]byte("ok"), 0, nil))

	_, err = tool.Call(context.Background(), json.RawMessage(`{"command":"rm file"}`))
	if err == nil {
		t.Fatal("expected policy denial error")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestShellTool_MockExecution(t *testing.T) {
	tool := newShellTool(Config{}, mockExecutor([]byte("hello\n"), 0, nil))

	got, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result Result
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit_code = %d", result.ExitCode)
	}
	if result.Output != "hello\n" {
		t.Fatalf("output = %q", result.Output)
	}
}

func mockExecutor(output []byte, code int, err error) executor {
	return func(_ context.Context, _ string, _ []string, _ string) ([]byte, int, error) {
		return output, code, err
	}
}
