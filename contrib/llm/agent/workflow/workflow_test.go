package workflow_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/workflow"
)

func TestLinearWorkflow(t *testing.T) {
	b := workflow.NewBuilder("test-linear")

	b.AddNode("greet", func(ctx context.Context, state *workflow.State) (string, error) {
		state.AppendMessage(llm.Message{Role: llm.Assistant, Content: "Hello!"})
		state.SetOutput(&llm.Response{Content: "Hello!"})
		return "", nil
	})

	b.AddNode("farewell", func(ctx context.Context, state *workflow.State) (string, error) {
		msgs := state.Messages()
		prev := msgs[len(msgs)-1].Content
		content := prev + " Goodbye!"
		state.SetOutput(&llm.Response{Content: content})
		return "", nil
	})

	b.SetEntryPoint("greet")
	b.AddEdge("greet", "farewell")
	b.SetFinishPoint("farewell")

	wf, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	eng := workflow.NewEngine(wf)
	resp, err := eng.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Content != "Hello! Goodbye!" {
		t.Errorf("content = %q, want 'Hello! Goodbye!'", resp.Content)
	}
}

func TestConditionalWorkflow(t *testing.T) {
	b := workflow.NewBuilder("test-conditional")

	b.AddNode("classify", func(_ context.Context, state *workflow.State) (string, error) {
		msgs := state.Messages()
		content := msgs[0].Content
		if content == "help" {
			return "support", nil
		}
		return "sales", nil
	})

	b.AddNode("support", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "Support response"})
		return "", nil
	})

	b.AddNode("sales", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "Sales response"})
		return "", nil
	})

	b.SetEntryPoint("classify")
	b.AddConditionalEdge("classify", map[string]workflow.NodeID{
		"support": "support",
		"sales":   "sales",
	}, "sales")
	b.SetFinishPoint("support")
	b.SetFinishPoint("sales")

	wf, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	eng := workflow.NewEngine(wf)

	resp, err := eng.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "help"}},
	})
	if err != nil {
		t.Fatalf("Run(help): %v", err)
	}
	if resp.Content != "Support response" {
		t.Errorf("content = %q, want 'Support response'", resp.Content)
	}

	resp, err = eng.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "buy"}},
	})
	if err != nil {
		t.Fatalf("Run(buy): %v", err)
	}
	if resp.Content != "Sales response" {
		t.Errorf("content = %q, want 'Sales response'", resp.Content)
	}
}

func TestRunIter(t *testing.T) {
	b := workflow.NewBuilder("test-iter")
	b.AddNode("work", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "done"})
		return "", nil
	})
	b.SetEntryPoint("work")
	b.SetFinishPoint("work")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	eng := workflow.NewEngine(wf)
	var steps int
	var final *llm.Response
	for ev, err := range eng.RunIter(context.Background(), llm.Request{}) {
		if err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case agent.EventStep:
			steps++
		case agent.EventFinal:
			final = ev.Response
		}
	}
	if steps != 1 {
		t.Errorf("steps = %d, want 1", steps)
	}
	if final == nil || final.Content != "done" {
		t.Errorf("final = %v", final)
	}
}

func TestMaxStepsGuard(t *testing.T) {
	b := workflow.NewBuilder("test-loop")
	b.AddNode("loop", func(_ context.Context, _ *workflow.State) (string, error) {
		return "loop", nil
	})
	b.SetEntryPoint("loop")
	b.AddConditionalEdge("loop", map[string]workflow.NodeID{"loop": "loop"}, "loop")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	eng := workflow.NewEngine(wf, workflow.WithMaxSteps(5))
	_, err = eng.Run(context.Background(), llm.Request{})
	if err == nil {
		t.Fatal("expected max steps error")
	}
}

func TestStateTypeSafe(t *testing.T) {
	state := workflow.NewState()
	state.Set("count", 42)
	state.Set("name", "test")

	count, ok := workflow.GetTyped[int](state, "count")
	if !ok || count != 42 {
		t.Errorf("count = %d, ok = %v", count, ok)
	}

	name, ok := workflow.GetTyped[string](state, "name")
	if !ok || name != "test" {
		t.Errorf("name = %q, ok = %v", name, ok)
	}

	_, ok = workflow.GetTyped[int](state, "missing")
	if ok {
		t.Error("should return false for missing key")
	}

	_, ok = workflow.GetTyped[int](state, "name")
	if ok {
		t.Error("should return false for wrong type")
	}
}

func TestBuildErrors(t *testing.T) {
	b := workflow.NewBuilder("test-errors")
	b.AddNode("a", nil)
	b.AddNode("a", nil) // duplicate

	_, err := b.Build()
	if err == nil {
		t.Fatal("expected build error for duplicate node")
	}
}

func TestNoEntryPoint(t *testing.T) {
	b := workflow.NewBuilder("test-no-entry")
	b.AddNode("a", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })

	_, err := b.Build()
	if err == nil {
		t.Fatal("expected build error for no entry point")
	}
}

func TestCheckpointCallback(t *testing.T) {
	b := workflow.NewBuilder("test-cp")
	b.AddNode("a", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "a"})
		return "", nil
	})
	b.SetEntryPoint("a")
	b.SetFinishPoint("a")
	wf, _ := b.Build()

	var checkpoints []*workflow.Checkpoint
	eng := workflow.NewEngine(wf, workflow.WithCheckpointFunc(func(_ context.Context, cp *workflow.Checkpoint) error {
		checkpoints = append(checkpoints, cp)
		return nil
	}))

	_, err := eng.Run(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 {
		t.Errorf("checkpoints = %d, want 1", len(checkpoints))
	}
	if checkpoints[0].CurrentNode != "a" {
		t.Errorf("checkpoint node = %q, want 'a'", checkpoints[0].CurrentNode)
	}
}

func TestTransformNode(t *testing.T) {
	b := workflow.NewBuilder("test-transform")
	b.AddNode("transform", workflow.TransformNode(func(_ context.Context, state *workflow.State) error {
		state.Set("transformed", true)
		state.SetOutput(&llm.Response{Content: "transformed"})
		return nil
	}))
	b.SetEntryPoint("transform")
	b.SetFinishPoint("transform")
	wf, _ := b.Build()

	eng := workflow.NewEngine(wf)
	resp, err := eng.Run(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "transformed" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestConditionNode(t *testing.T) {
	decide := workflow.ConditionNode(func(_ context.Context, state *workflow.State) (string, error) {
		msgs := state.Messages()
		if len(msgs) > 0 && msgs[0].Content == "yes" {
			return "approved", nil
		}
		return "rejected", nil
	})

	state := workflow.NewState()
	state.AppendMessage(llm.Message{Role: llm.User, Content: "yes"})
	key, err := decide(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if key != "approved" {
		t.Errorf("key = %q, want 'approved'", key)
	}
}

func TestOnStepCallback(t *testing.T) {
	b := workflow.NewBuilder("test-onstep")
	b.AddNode("a", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "done"})
		return "", nil
	})
	b.SetEntryPoint("a")
	b.SetFinishPoint("a")
	wf, _ := b.Build()

	var stepNodes []string
	eng := workflow.NewEngine(wf, workflow.WithOnStep(func(step int, nodeID workflow.NodeID) {
		stepNodes = append(stepNodes, fmt.Sprintf("%d:%s", step, nodeID))
	}))

	_, err := eng.Run(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stepNodes) != 1 || stepNodes[0] != "1:a" {
		t.Errorf("stepNodes = %v", stepNodes)
	}
}
