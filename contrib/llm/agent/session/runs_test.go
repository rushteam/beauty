package session_test

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
)

func TestRunsPageCursors(t *testing.T) {
	sess := &session.Session{}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		sess.Runs = append(sess.Runs, session.Run{
			Index:     i,
			ID:        string(rune('a' + i)),
			Status:    agent.StatusDone,
			StartedAt: start.Add(time.Duration(i) * time.Hour),
		})
	}
	sess.Runs = append(sess.Runs, session.Run{
		Index: 5, ID: "child", ParentID: "c", Status: agent.StatusDone, StartedAt: start,
	})

	page, err := sess.RunsPage(session.RunQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 || !page.HasMore || page.Oldest != 3 || page.Newest != 4 || len(page.Runs) != 2 {
		t.Fatalf("newest page = %+v", page)
	}

	before := page.Oldest
	older, err := sess.RunsPage(session.RunQuery{Limit: 2, Before: &before})
	if err != nil {
		t.Fatal(err)
	}
	if older.Oldest != 1 || older.Newest != 2 || !older.HasMore {
		t.Fatalf("older page = %+v", older)
	}

	after := older.Newest
	newer, err := sess.RunsPage(session.RunQuery{Limit: 2, After: &after})
	if err != nil {
		t.Fatal(err)
	}
	if newer.Oldest != 3 || newer.Newest != 4 || newer.HasMore {
		t.Fatalf("newer page = %+v", newer)
	}

	both := 0
	if _, err := sess.RunsPage(session.RunQuery{Before: &both, After: &both}); !errors.Is(err, session.ErrBothCursors) {
		t.Fatalf("cursors = %v", err)
	}

	doneOnly, err := sess.RunsPage(session.RunQuery{Status: []agent.RunStatus{agent.StatusPaused}})
	if err != nil || doneOnly.Total != 0 {
		t.Fatalf("status filter = %+v %v", doneOnly, err)
	}

	mid, err := sess.RunsPage(session.RunQuery{Since: start.Add(2 * time.Hour), Until: start.Add(3 * time.Hour)})
	if err != nil || mid.Total != 2 {
		t.Fatalf("time filter total = %d", mid.Total)
	}

	withChild, err := sess.RunsPage(session.RunQuery{Limit: 1, Before: intPtr(3)})
	if err != nil {
		t.Fatal(err)
	}
	if len(withChild.Runs) != 1 || withChild.Runs[0].ID != "c" || len(withChild.Runs[0].Children) != 1 {
		t.Fatalf("children = %+v", withChild.Runs)
	}
}

func TestManagerAccumulatesRunUsage(t *testing.T) {
	store := session.NewMemoryStore()
	mgr := &session.Manager{Store: store}
	a := &queueAgent{scripts: [][]agent.Event{
		{
			{Type: agent.EventStep, RunID: "m1", Response: &llm.Response{Usage: llm.Usage{InputTokens: 3, OutputTokens: 4}}},
			{Type: agent.EventStep, RunID: "r1", Response: &llm.Response{Usage: llm.Usage{InputTokens: 10, OutputTokens: 2}}},
			{Type: agent.EventFinal, RunID: "r1", Response: &llm.Response{Content: "ok", Usage: llm.Usage{InputTokens: 10, OutputTokens: 2}}},
		},
		{
			{Type: agent.EventStep, RunID: "r2", Response: &llm.Response{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}}},
			{Type: agent.EventFinal, RunID: "r2", Response: &llm.Response{Content: "next"}},
		},
	}}
	out := mgr.Run(context.Background(), "s", a, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
	if !out.IsDone() || out.Usage.InputTokens != 13 || out.Usage.OutputTokens != 6 {
		t.Fatalf("outcome usage = %+v status=%s", out.Usage, out.Status)
	}
	mgr.Run(context.Background(), "s", a, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q2"}}})

	sess, err := store.Load(context.Background(), "s")
	if err != nil || sess == nil {
		t.Fatal(err)
	}
	if sess.Usage.InputTokens != 14 || sess.Usage.OutputTokens != 7 {
		t.Fatalf("session usage = %+v", sess.Usage)
	}
	page, err := sess.RunsPage(session.RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("total = %d", page.Total)
	}
	if len(page.Runs[0].Children) != 1 || page.Runs[0].Children[0].ID != "m1" {
		t.Fatalf("first run = %+v", page.Runs[0])
	}
	if page.Runs[0].Usage.InputTokens != 13 {
		t.Fatalf("top usage counted child twice or not at all: %+v", page.Runs[0].Usage)
	}
}

func intPtr(n int) *int { return &n }

type queueAgent struct {
	scripts [][]agent.Event
	i       int
}

func (a *queueAgent) Run(context.Context, llm.Request, ...agent.Option) iter.Seq2[agent.Event, error] {
	return a.next()
}

func (a *queueAgent) Continue(context.Context, string, []agent.Resolution, ...agent.Option) iter.Seq2[agent.Event, error] {
	return a.next()
}

func (a *queueAgent) next() iter.Seq2[agent.Event, error] {
	var evs []agent.Event
	if a.i < len(a.scripts) {
		evs = a.scripts[a.i]
		a.i++
	}
	return func(yield func(agent.Event, error) bool) {
		for _, ev := range evs {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (a *queueAgent) Info() agent.Info { return agent.Info{Name: "queue"} }
