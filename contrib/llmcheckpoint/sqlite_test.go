package llmcheckpoint_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
	"github.com/rushteam/beauty/contrib/llmcheckpoint"
)

func TestSQLiteCheckpointPauseResume(t *testing.T) {
	store, err := llmcheckpoint.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	_ = store.AppendEvents(ctx, "run-1",
		checkpoint.NewEvent(checkpoint.TypeRunStarted, "run-1"),
		checkpoint.NewEvent(checkpoint.TypeUserMessage, "run-1"),
	)
	snap := &agent.RunSnapshot{
		Kind:       "runner",
		EventCount: 2,
		Request:    llm.Request{Model: "m"},
	}
	if err := store.Save(ctx, "run-1", snap); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx, "run-1")
	if err != nil || loaded == nil {
		t.Fatalf("load: err=%v snap=%v", err, loaded)
	}
	if loaded.EventCount != 2 {
		t.Fatalf("EventCount = %d", loaded.EventCount)
	}
	if err := store.Delete(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}
	events, err := store.LoadEvents(ctx, "run-1")
	if err != nil || len(events) != 2 {
		t.Fatalf("events after delete snap: err=%v n=%d", err, len(events))
	}
}

func TestSQLiteSnapshotRoundTrip(t *testing.T) {
	store, err := llmcheckpoint.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snap := &agent.RunSnapshot{
		Kind:    "chain",
		Request: llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}},
	}
	raw, _ := json.Marshal(snap)
	if len(raw) == 0 {
		t.Fatal("marshal failed")
	}
	if err := store.Save(context.Background(), "r1", snap); err != nil {
		t.Fatal(err)
	}
}
