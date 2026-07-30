package llmsession_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
	"github.com/rushteam/beauty/contrib/llmsession"
)

func TestSQLiteStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	store, err := llmsession.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if s, err := store.Load(ctx, "a"); err != nil || s != nil {
		t.Fatalf("empty: %v %+v", err, s)
	}
	in := &session.Session{
		ID:        "a",
		Summary:   "sum",
		Messages:  []llm.Message{{Role: llm.User, Content: "hi"}, {Role: llm.Assistant, Content: "yo"}},
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := store.Load(ctx, "a")
	if err != nil || out == nil {
		t.Fatalf("load: %v %v", err, out)
	}
	if out.Summary != "sum" || len(out.Messages) != 2 || out.Messages[1].Content != "yo" {
		t.Fatalf("got %+v", out)
	}
	// reopen
	store.Close()
	store2, err := llmsession.NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	out2, err := store2.Load(ctx, "a")
	if err != nil || out2 == nil || out2.Summary != "sum" {
		t.Fatalf("reopen: %v %+v", err, out2)
	}
}
