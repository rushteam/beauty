package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// RepairJSON 覆盖常见的模型笔误,修复结果必须重新通过 json.Valid。
func TestRepairJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any // 修复后反序列化应相等(仅对象类用例填);nil 表示只校验合法性
	}{
		{"trailing comma", `{"a":1,"b":2,}`, map[string]any{"a": 1.0, "b": 2.0}},
		{"single quotes", `{'a':'x'}`, map[string]any{"a": "x"}},
		{"bare keys", `{a:1, b:"y"}`, map[string]any{"a": 1.0, "b": "y"}},
		{"code fence", "```json\n{\"a\":1}\n```", map[string]any{"a": 1.0}},
		{"py constants", `{"ok":True,"no":False,"nil":None}`, map[string]any{"ok": true, "no": false, "nil": nil}},
		{"line comment", "{\"a\":1 // note\n}", map[string]any{"a": 1.0}},
		{"block comment", `{"a":/* c */1}`, map[string]any{"a": 1.0}},
		{"prose around", `Sure! {"a":1} hope that helps`, map[string]any{"a": 1.0}},
		{"trailing comma in array", `{"xs":[1,2,3,]}`, nil},
		{"leading dot number", `{"n":.5}`, map[string]any{"n": 0.5}},
		{"nested + mixed", `{a:{'b':[1,2,],},c:True,}`, nil},
		{"unescaped newline", "{\"a\":\"li\nne\"}", map[string]any{"a": "li\nne"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := agent.RepairJSON([]byte(c.in))
			if !ok {
				t.Fatalf("修复失败: %q", c.in)
			}
			if !json.Valid(out) {
				t.Fatalf("修复结果非法 JSON: %q -> %q", c.in, out)
			}
			if c.want != nil {
				var got map[string]any
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatalf("反序列化: %v (%q)", err, out)
				}
				if len(got) != len(c.want) {
					t.Fatalf("字段数不符: got %v want %v", got, c.want)
				}
				for k, v := range c.want {
					if got[k] != v {
						t.Fatalf("键 %q: got %#v want %#v (整体 %q)", k, got[k], v, out)
					}
				}
			}
		})
	}
}

// 已经合法的 JSON 也应原样修复通过(幂等)。
func TestRepairJSON_AlreadyValid(t *testing.T) {
	in := `{"a":1,"b":["x","y"],"c":{"d":true}}`
	out, ok := agent.RepairJSON([]byte(in))
	if !ok || !json.Valid(out) {
		t.Fatalf("合法输入应修复通过: ok=%v out=%q", ok, out)
	}
}

// 无法救回的输入返回 ok=false(调用方保留原文)。
func TestRepairJSON_Unrepairable(t *testing.T) {
	if _, ok := agent.RepairJSON([]byte("this is not json at all")); ok {
		t.Fatal("纯散文不应被判为修复成功")
	}
}

// 集成:RepairToolArgs 让工具循环容忍模型给出的坏 JSON 参数(尾逗号+单引号)。
func TestRunner_RepairToolArgs(t *testing.T) {
	var seen string
	tool := agent.Func("echo", "回显", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		seen = string(args)
		return "ok", nil
	})
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{'x':1,}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{tool}, RepairToolArgs: true}
	if _, err := r.Run(context.Background(), llm.Request{Model: "m"}).Final(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !json.Valid([]byte(seen)) {
		t.Fatalf("工具应收到修复后的合法 JSON, got %q", seen)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(seen), &got)
	if got["x"] != 1.0 {
		t.Fatalf("修复后参数不对: %q", seen)
	}
}

// 关闭修复时(默认),坏 JSON 原样传给工具(工具自行处理)。
func TestRunner_RepairToolArgs_Disabled(t *testing.T) {
	var seen string
	tool := agent.Func("echo", "", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		seen = string(args)
		return "ok", nil
	})
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{'x':1,}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{tool}} // RepairToolArgs 默认 false
	if _, err := r.Run(context.Background(), llm.Request{Model: "m"}).Final(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if seen != `{'x':1,}` {
		t.Fatalf("未开启修复时应原样透传, got %q", seen)
	}
}
