package agent_test

import (
	"context"
	"iter"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestGetOption(t *testing.T) {
	opts := []agent.Option{
		agent.WithModel("gpt-4o"),
		agent.WithMaxSteps(16),
		agent.WithTemperature(0.7),
	}

	model, ok := agent.GetOption[agent.WithModel](opts)
	if !ok {
		t.Fatal("WithModel not found")
	}
	if string(model) != "gpt-4o" {
		t.Errorf("model = %q, want 'gpt-4o'", model)
	}

	steps, ok := agent.GetOption[agent.WithMaxSteps](opts)
	if !ok {
		t.Fatal("WithMaxSteps not found")
	}
	if int(steps) != 16 {
		t.Errorf("steps = %d, want 16", steps)
	}

	temp, ok := agent.GetOption[agent.WithTemperature](opts)
	if !ok {
		t.Fatal("WithTemperature not found")
	}
	if float64(temp) != 0.7 {
		t.Errorf("temp = %f, want 0.7", temp)
	}
}

func TestGetOption_LastWins(t *testing.T) {
	opts := []agent.Option{
		agent.WithModel("gpt-3.5"),
		agent.WithModel("gpt-4o"),
	}

	model, ok := agent.GetOption[agent.WithModel](opts)
	if !ok {
		t.Fatal("WithModel not found")
	}
	if string(model) != "gpt-4o" {
		t.Errorf("model = %q, want 'gpt-4o' (last wins)", model)
	}
}

func TestGetOption_Empty(t *testing.T) {
	_, ok := agent.GetOption[agent.WithModel](nil)
	if ok {
		t.Error("should not find option in nil slice")
	}

	_, ok = agent.GetOption[agent.WithModel]([]agent.Option{})
	if ok {
		t.Error("should not find option in empty slice")
	}
}

func TestGetOption_WithToolChoice(t *testing.T) {
	opts := []agent.Option{
		agent.WithModel("gpt-4o"),
		agent.WithToolChoice("required"),
	}
	tc, ok := agent.GetOption[agent.WithToolChoice](opts)
	if !ok {
		t.Fatal("WithToolChoice not found")
	}
	if string(tc) != "required" {
		t.Errorf("toolChoice = %q, want 'required'", tc)
	}
}

type toolChoiceClient struct {
	last llm.Request
}

func (c *toolChoiceClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.last = req
	return &llm.Response{Content: "ok"}, nil
}

func (c *toolChoiceClient) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {}
}

func TestApplyOptions_WithToolChoice(t *testing.T) {
	fc := &toolChoiceClient{}
	r := &agent.Runner{Client: fc}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}, agent.WithToolChoice("none")))
	if _, err := out.Final(); err != nil {
		t.Fatal(err)
	}
	if fc.last.ToolChoice != "none" {
		t.Fatalf("ToolChoice = %q, want 'none'", fc.last.ToolChoice)
	}
}
