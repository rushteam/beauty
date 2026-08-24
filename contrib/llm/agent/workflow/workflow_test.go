package workflow_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
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

func TestBuildValidatesEdgeTargets(t *testing.T) {
	b := workflow.NewBuilder("test-edge-validation")
	b.AddNode("a", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })
	b.SetEntryPoint("a")
	b.AddEdge("a", "nonexistent")

	_, err := b.Build()
	if err == nil {
		t.Fatal("expected build error for edge to unknown node")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention unknown node: %v", err)
	}
}

func TestBuildValidatesConditionalEdgeTargets(t *testing.T) {
	b := workflow.NewBuilder("test-cond-validation")
	b.AddNode("a", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })
	b.SetEntryPoint("a")
	b.AddConditionalEdge("a", map[string]workflow.NodeID{
		"x": "ghost",
	}, "")

	_, err := b.Build()
	if err == nil {
		t.Fatal("expected build error for conditional edge to unknown node")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention unknown node: %v", err)
	}
}

func TestBuildRejectsMultipleRoutingEdges(t *testing.T) {
	b := workflow.NewBuilder("test-multi-route")
	b.AddNode("a", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })
	b.AddNode("b", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })
	b.AddNode("c", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })
	b.SetEntryPoint("a")
	b.AddEdge("a", "b")
	b.AddEdge("a", "c")

	_, err := b.Build()
	if err == nil {
		t.Fatal("expected build error for multiple routing edges")
	}
	if !strings.Contains(err.Error(), "routing edges") {
		t.Errorf("error should mention routing edges: %v", err)
	}
}

func TestFanOutPropagatesContext(t *testing.T) {
	type ctxKey struct{}
	b := workflow.NewBuilder("test-fanout-ctx")

	var gotValue atomic.Value

	b.AddNode("start", func(_ context.Context, state *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("worker", func(ctx context.Context, state *workflow.State) (string, error) {
		if v := ctx.Value(ctxKey{}); v != nil {
			gotValue.Store(v)
		}
		state.SetOutput(&llm.Response{Content: "done"})
		return "", nil
	})
	b.SetEntryPoint("start")
	b.AddFanOut("start", "worker")
	b.SetFinishPoint("worker")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), ctxKey{}, "hello")
	eng := workflow.NewEngine(wf)
	_, err = eng.Run(ctx, llm.Request{})
	if err != nil {
		t.Fatal(err)
	}

	v := gotValue.Load()
	if v != "hello" {
		t.Errorf("fan-out node did not receive parent context value: got %v", v)
	}
}

func TestFanOutCancellation(t *testing.T) {
	b := workflow.NewBuilder("test-fanout-cancel")

	b.AddNode("start", func(_ context.Context, _ *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("blocker", func(ctx context.Context, _ *workflow.State) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	b.SetEntryPoint("start")
	b.AddFanOut("start", "blocker")
	b.SetFinishPoint("blocker")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eng := workflow.NewEngine(wf)
	_, err = eng.Run(ctx, llm.Request{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestFanOutJoinsAllErrors(t *testing.T) {
	b := workflow.NewBuilder("test-fanout-errors")

	b.AddNode("start", func(_ context.Context, _ *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("fail1", func(_ context.Context, _ *workflow.State) (string, error) {
		return "", fmt.Errorf("error-one")
	})
	b.AddNode("fail2", func(_ context.Context, _ *workflow.State) (string, error) {
		return "", fmt.Errorf("error-two")
	})
	b.SetEntryPoint("start")
	b.AddFanOut("start", "fail1", "fail2")
	b.SetFinishPoint("fail1")
	b.SetFinishPoint("fail2")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	eng := workflow.NewEngine(wf)
	_, err = eng.Run(context.Background(), llm.Request{})
	if err == nil {
		t.Fatal("expected fan-out error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "error-one") || !strings.Contains(errStr, "error-two") {
		t.Errorf("error should contain both errors: %v", err)
	}
}

func TestFanOutEmitsStepEvents(t *testing.T) {
	b := workflow.NewBuilder("test-fanout-events")

	b.AddNode("start", func(_ context.Context, _ *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("w1", func(_ context.Context, state *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("w2", func(_ context.Context, state *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("merge", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "merged"})
		return "", nil
	})
	b.SetEntryPoint("start")
	b.AddFanOut("start", "w1", "w2")
	b.AddFanIn([]workflow.NodeID{"w1", "w2"}, "merge")
	b.SetFinishPoint("merge")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	eng := workflow.NewEngine(wf)
	var stepNames []string
	for ev, err := range eng.RunIter(context.Background(), llm.Request{}) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Type == agent.EventStep {
			stepNames = append(stepNames, ev.AgentName)
		}
	}
	// start + w1 + w2 (fan-out step events) + merge = 4 step events
	if len(stepNames) != 4 {
		t.Errorf("stepNames = %v, want 4 steps", stepNames)
	}
}

func TestWithMaxFanOut(t *testing.T) {
	b := workflow.NewBuilder("test-maxfanout")

	var peak atomic.Int32
	var running atomic.Int32

	makeWorker := func(name string) workflow.NodeFunc {
		return func(_ context.Context, state *workflow.State) (string, error) {
			cur := running.Add(1)
			defer running.Add(-1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			return "", nil
		}
	}

	b.AddNode("start", func(_ context.Context, _ *workflow.State) (string, error) {
		return "", nil
	})
	b.AddNode("w1", makeWorker("w1"))
	b.AddNode("w2", makeWorker("w2"))
	b.AddNode("w3", makeWorker("w3"))
	b.AddNode("w4", makeWorker("w4"))
	b.AddNode("done", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "done"})
		return "", nil
	})
	b.SetEntryPoint("start")
	b.AddFanOut("start", "w1", "w2", "w3", "w4")
	b.AddFanIn([]workflow.NodeID{"w1", "w2", "w3", "w4"}, "done")
	b.SetFinishPoint("done")

	wf, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	eng := workflow.NewEngine(wf, workflow.WithMaxFanOut(2))
	resp, err := eng.Run(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "done" {
		t.Errorf("content = %q", resp.Content)
	}
	if p := peak.Load(); p > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", p)
	}
}

func TestCompletedMapLazyInit(t *testing.T) {
	b := workflow.NewBuilder("test-lazy-completed")
	b.AddNode("a", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "ok"})
		return "", nil
	})
	b.SetEntryPoint("a")
	b.SetFinishPoint("a")
	wf, _ := b.Build()

	eng := workflow.NewEngine(wf)
	resp, err := eng.Run(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestCheckpointHasCompletedMap(t *testing.T) {
	b := workflow.NewBuilder("test-cp-completed")
	b.AddNode("a", func(_ context.Context, state *workflow.State) (string, error) {
		state.SetOutput(&llm.Response{Content: "ok"})
		return "", nil
	})
	b.SetEntryPoint("a")
	b.SetFinishPoint("a")
	wf, _ := b.Build()

	var cps []*workflow.Checkpoint
	eng := workflow.NewEngine(wf, workflow.WithCheckpointFunc(func(_ context.Context, cp *workflow.Checkpoint) error {
		cps = append(cps, cp)
		return nil
	}))

	_, err := eng.Run(context.Background(), llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cps) != 1 {
		t.Fatalf("checkpoints = %d", len(cps))
	}
	if !cps[0].Completed["a"] {
		t.Error("checkpoint should have node 'a' completed")
	}
}

func TestMessagesLen(t *testing.T) {
	state := workflow.NewState()
	if n := state.MessagesLen(); n != 0 {
		t.Errorf("MessagesLen = %d, want 0", n)
	}
	state.AppendMessage(llm.Message{Role: llm.User, Content: "hi"})
	if n := state.MessagesLen(); n != 1 {
		t.Errorf("MessagesLen = %d, want 1", n)
	}
}

func TestBuildValidatesFanOutTargets(t *testing.T) {
	b := workflow.NewBuilder("test-fanout-validation")
	b.AddNode("a", func(_ context.Context, _ *workflow.State) (string, error) { return "", nil })
	b.SetEntryPoint("a")
	b.AddFanOut("a", "ghost1", "ghost2")

	_, err := b.Build()
	if err == nil {
		t.Fatal("expected build error for fan-out to unknown nodes")
	}
	if !strings.Contains(err.Error(), "ghost1") {
		t.Errorf("error should mention ghost1: %v", err)
	}
}
