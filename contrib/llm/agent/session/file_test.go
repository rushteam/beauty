package session_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
)

func TestFileStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if s, err := store.Load(ctx, "u1"); err != nil || s != nil {
		t.Fatalf("empty load: %v %+v", err, s)
	}
	in := &session.Session{
		ID:      "u1",
		Summary: "hello",
		Messages: []llm.Message{
			{Role: llm.User, Content: "hi"},
			{Role: llm.Assistant, Content: "yo"},
		},
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := store.Load(ctx, "u1")
	if err != nil || out == nil {
		t.Fatalf("load: %v %v", err, out)
	}
	if out.Summary != "hello" || len(out.Messages) != 2 || out.Messages[0].Content != "hi" {
		t.Fatalf("got %+v", out)
	}
	// 持久化到文件
	if _, err := filepath.Glob(filepath.Join(dir, "*.json")); err != nil {
		t.Fatal(err)
	}
}

func TestFileStore_RejectBadID(t *testing.T) {
	store, err := session.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background(), "../etc/passwd")
	if err == nil {
		t.Fatal("should reject path traversal id")
	}
}
