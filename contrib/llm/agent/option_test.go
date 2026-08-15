package agent_test

import (
	"testing"

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

	_, ok = agent.GetOption[agent.WithStreaming](opts)
	if ok {
		t.Error("WithStreaming should not be found")
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
