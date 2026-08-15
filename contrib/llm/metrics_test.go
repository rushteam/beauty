package llm_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

func TestCostTracker_RecordAndSummary(t *testing.T) {
	tracker := llm.NewCostTracker()
	tracker.Record(llm.RolePrimary, "gpt-4o", llm.Usage{InputTokens: 10, OutputTokens: 5}, 100*time.Millisecond)
	tracker.Record(llm.RolePrimary, "gpt-4o", llm.Usage{InputTokens: 3, OutputTokens: 2}, 50*time.Millisecond)
	tracker.Record(llm.RoleCompression, "gpt-4o-mini", llm.Usage{InputTokens: 100, OutputTokens: 20}, 200*time.Millisecond)

	summary := tracker.Summary()
	if len(summary) != 2 {
		t.Fatalf("summary len = %d, want 2", len(summary))
	}

	// 按 role 字典序: compression < primary
	if summary[0].Role != llm.RoleCompression {
		t.Fatalf("summary[0].Role = %q, want compression", summary[0].Role)
	}
	if summary[0].Model != "gpt-4o-mini" || summary[0].Calls != 1 {
		t.Fatalf("compression usage = %+v", summary[0])
	}
	if summary[0].Usage.InputTokens != 100 || summary[0].Usage.OutputTokens != 20 {
		t.Fatalf("compression tokens = %+v", summary[0].Usage)
	}
	if summary[0].TotalLatency != 200*time.Millisecond {
		t.Fatalf("compression latency = %v", summary[0].TotalLatency)
	}

	if summary[1].Role != llm.RolePrimary {
		t.Fatalf("summary[1].Role = %q, want primary", summary[1].Role)
	}
	if summary[1].Calls != 2 {
		t.Fatalf("primary calls = %d, want 2", summary[1].Calls)
	}
	if summary[1].Usage.InputTokens != 13 || summary[1].Usage.OutputTokens != 7 {
		t.Fatalf("primary tokens = %+v", summary[1].Usage)
	}
	if summary[1].TotalLatency != 150*time.Millisecond {
		t.Fatalf("primary latency = %v", summary[1].TotalLatency)
	}

	// Summary 应返回快照副本,后续修改不影响已返回结果。
	summary[0].Calls = 999
	fresh := tracker.Summary()
	if fresh[0].Calls != 1 {
		t.Fatalf("summary should be a copy, got Calls=%d", fresh[0].Calls)
	}
}

func TestCostTracker_ConcurrentRecord(t *testing.T) {
	tracker := llm.NewCostTracker()
	const goroutines = 32
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				tracker.Record(llm.RolePrimary, "m", llm.Usage{InputTokens: 1, OutputTokens: 1}, time.Millisecond)
			}
		}()
	}
	wg.Wait()

	in, out := tracker.Total()
	want := goroutines * perGoroutine
	if in != want || out != want {
		t.Fatalf("Total() = (%d, %d), want (%d, %d)", in, out, want, want)
	}

	summary := tracker.Summary()
	if len(summary) != 1 {
		t.Fatalf("summary len = %d, want 1", len(summary))
	}
	if summary[0].Calls != want {
		t.Fatalf("Calls = %d, want %d", summary[0].Calls, want)
	}
}

func TestCostTracker_Total(t *testing.T) {
	tracker := llm.NewCostTracker()
	tracker.Record(llm.RolePrimary, "a", llm.Usage{InputTokens: 10, OutputTokens: 5}, 0)
	tracker.Record(llm.RolePlanner, "b", llm.Usage{InputTokens: 20, OutputTokens: 15}, 0)

	in, out := tracker.Total()
	if in != 30 || out != 20 {
		t.Fatalf("Total() = (%d, %d), want (30, 20)", in, out)
	}
}

func TestCostTracker_Reset(t *testing.T) {
	tracker := llm.NewCostTracker()
	tracker.Record(llm.RolePrimary, "m", llm.Usage{InputTokens: 10, OutputTokens: 5}, time.Second)
	tracker.Reset()

	if summary := tracker.Summary(); len(summary) != 0 {
		t.Fatalf("after Reset summary len = %d, want 0", len(summary))
	}
	in, out := tracker.Total()
	if in != 0 || out != 0 {
		t.Fatalf("after Reset Total() = (%d, %d), want (0, 0)", in, out)
	}
}

func TestWithRole_RecordsUsage(t *testing.T) {
	tracker := llm.NewCostTracker()

	genClient := llm.WithRole(openaiMock(t), tracker, llm.RoleSummarizer)
	_, err := genClient.Generate(context.Background(), llm.Request{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	streamClient := llm.WithRole(&stubClient{chunks: []string{"a", "b"}}, tracker, llm.RoleSummarizer)
	for _, err := range streamClient.Stream(context.Background(), llm.Request{
		Model:    "stream-model",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	}) {
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
	}

	summary := tracker.Summary()
	if len(summary) != 1 {
		t.Fatalf("summary len = %d, want 1", len(summary))
	}
	ru := summary[0]
	if ru.Role != llm.RoleSummarizer {
		t.Fatalf("role = %q, want summarizer", ru.Role)
	}
	if ru.Calls != 2 {
		t.Fatalf("calls = %d, want 2", ru.Calls)
	}
	if ru.Usage.InputTokens != 13 || ru.Usage.OutputTokens != 22 {
		t.Fatalf("usage = %+v, want input=13 output=22", ru.Usage)
	}
	if ru.Model != "stream-model" {
		t.Fatalf("model = %q, want stream-model (last call wins)", ru.Model)
	}
}

func TestWithCostTracker_RoundTrip(t *testing.T) {
	tracker := llm.NewCostTracker()
	ctx := llm.WithCostTracker(context.Background(), tracker)

	got := llm.CostTrackerFrom(ctx)
	if got != tracker {
		t.Fatal("CostTrackerFrom should return the same tracker")
	}
	if llm.CostTrackerFrom(context.Background()) != nil {
		t.Fatal("CostTrackerFrom on bare context should return nil")
	}
}
