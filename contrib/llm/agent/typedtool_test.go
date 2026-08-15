package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type weatherInput struct {
	City string `json:"city"`
}

type weatherOutput struct {
	Temp int    `json:"temp"`
	Cond string `json:"cond"`
}

func TestTypedFunc_BasicCall(t *testing.T) {
	tool, err := agent.TypedFunc[weatherInput, weatherOutput]("get_weather", "查天气",
		func(_ context.Context, in weatherInput) (weatherOutput, error) {
			if in.City != "北京" {
				t.Fatalf("city = %q, want 北京", in.City)
			}
			return weatherOutput{Temp: 25, Cond: "晴"}, nil
		})
	if err != nil {
		t.Fatalf("TypedFunc: %v", err)
	}

	got, err := tool.Call(context.Background(), json.RawMessage(`{"city":"北京"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != `{"temp":25,"cond":"晴"}` {
		t.Fatalf("result = %q", got)
	}
}

func TestTypedFunc_StringOutput(t *testing.T) {
	tool, err := agent.TypedFunc[weatherInput, string]("echo_city", "回显城市",
		func(_ context.Context, in weatherInput) (string, error) {
			return "city:" + in.City, nil
		})
	if err != nil {
		t.Fatalf("TypedFunc: %v", err)
	}

	got, err := tool.Call(context.Background(), json.RawMessage(`{"city":"上海"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "city:上海" {
		t.Fatalf("result = %q, want plain string without JSON quoting", got)
	}
}

func TestTypedFunc_SchemaGeneration(t *testing.T) {
	tool, err := agent.TypedFunc[weatherInput, weatherOutput]("get_weather", "查天气",
		func(_ context.Context, in weatherInput) (weatherOutput, error) {
			return weatherOutput{}, nil
		})
	if err != nil {
		t.Fatalf("TypedFunc: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.Def.Parameters, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T", schema["properties"])
	}
	city, ok := props["city"].(map[string]any)
	if !ok || city["type"] != "string" {
		t.Fatalf("city property = %v", props["city"])
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "city" {
		t.Fatalf("required = %v, want [city]", schema["required"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
}

func TestMustTypedFunc_PanicsOnInvalidInputType(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for non-struct input type")
		}
		if !strings.Contains(fmt.Sprint(r), "struct") {
			t.Fatalf("panic = %v, want struct validation error", r)
		}
	}()
	_ = agent.MustTypedFunc[int, string]("bad", "", func(_ context.Context, _ int) (string, error) {
		return "", nil
	})
}

func TestTypedFunc_WithPermission(t *testing.T) {
	tool, err := agent.TypedFunc[weatherInput, string]("gated", "需审批",
		func(_ context.Context, _ weatherInput) (string, error) { return "ok", nil },
		agent.WithToolPermission(agent.PermitAsk))
	if err != nil {
		t.Fatalf("TypedFunc: %v", err)
	}
	if tool.Permission != agent.PermitAsk {
		t.Fatalf("Permission = %v, want PermitAsk", tool.Permission)
	}
}

func TestTypedFunc_RunnerIntegration(t *testing.T) {
	tool := agent.MustTypedFunc[weatherInput, weatherOutput]("get_weather", "查天气",
		func(_ context.Context, in weatherInput) (weatherOutput, error) {
			return weatherOutput{Temp: 25, Cond: in.City + "晴"}, nil
		})

	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"北京"}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{tool}}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "go"}}}))
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("final content = %q", resp.Content)
	}
	msgs := fc.lastReq.Messages
	if len(msgs) != 3 {
		t.Fatalf("第二轮消息数 = %d, want 3", len(msgs))
	}
	wantResult := `{"temp":25,"cond":"北京晴"}`
	if msgs[2].Role != llm.Tool || msgs[2].Content != wantResult {
		t.Fatalf("工具结果 = %+v, want %q", msgs[2], wantResult)
	}
	if len(fc.lastReq.Tools) != 1 || fc.lastReq.Tools[0].Name != "get_weather" {
		t.Fatalf("tools 未注入: %+v", fc.lastReq.Tools)
	}
}
