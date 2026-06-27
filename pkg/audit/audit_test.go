package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/audit"
)

const (
	resUser audit.Resource = iota + 1
	resConfig
)

type memSink struct {
	mu   sync.Mutex
	logs []audit.Entry
}

func (m *memSink) Write(ctx context.Context, e audit.Entry) error {
	m.mu.Lock()
	m.logs = append(m.logs, e)
	m.mu.Unlock()
	return nil
}

func (m *memSink) snapshot() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]audit.Entry, len(m.logs))
	copy(out, m.logs)
	return out
}

func TestAudit_HTTPSuccess(t *testing.T) {
	sink := &memSink{}
	a := audit.New(sink)
	defer a.Stop()

	mux := http.NewServeMux()
	mux.Handle("/users", a.HTTPMiddleware(func(r *http.Request) (audit.Resource, string, string) {
		return resUser, "u1", `{"ip":"1.2.3.4"}`
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/users", nil).WithContext(
		audit.WithUserID(context.Background(), "admin"),
	))
	a.Stop() // flush 异步队列

	logs := sink.snapshot()
	if len(logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(logs))
	}
	l := logs[0]
	if l.UserID != "admin" || l.Resource != resUser || l.ResourceID != "u1" {
		t.Fatalf("entry mismatch: %+v", l)
	}
	if l.Action != audit.ActionUpdate || l.Status != 200 || l.Path != "/users" {
		t.Fatalf("entry fields: %+v", l)
	}
	if l.ID != 1 {
		t.Fatalf("id=%d", l.ID)
	}
}

func TestAudit_HTTP5xxNotLogged(t *testing.T) {
	sink := &memSink{}
	a := audit.New(sink)
	defer a.Stop()

	h := a.HTTPMiddleware(func(*http.Request) (audit.Resource, string, string) {
		return resConfig, "x", ""
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if len(sink.snapshot()) != 0 {
		t.Fatal("5xx should not be audited")
	}
}

func TestAudit_NilResolverNoLog(t *testing.T) {
	sink := &memSink{}
	a := audit.New(sink)
	defer a.Stop()

	h := a.HTTPMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if len(sink.snapshot()) != 0 {
		t.Fatal("nil resolver should not log")
	}
}

func TestAudit_AsyncDispatch(t *testing.T) {
	var count atomic.Int32
	sink := audit.SinkFunc(func(ctx context.Context, e audit.Entry) error {
		count.Add(1)
		return nil
	})
	a := audit.New(sink, audit.WithQueueSize(8))
	defer a.Stop()

	for range 20 {
		a.Record(context.Background(), audit.Entry{UserID: "u", Resource: resUser, Action: audit.ActionRead})
	}
	// 队列 8,发了 20:dispatch 边发边消费,部分落盘部分丢弃。
	a.Stop() // flush
	got := count.Load()
	if got < 1 || got > 20 {
		t.Fatalf("dispatched count=%d, want 1..20", got)
	}
}

func TestAudit_StopIdempotent(t *testing.T) {
	a := audit.New(nil)
	a.Stop()
	a.Stop() // 不 panic
}

func TestAudit_ActionMapping(t *testing.T) {
	cases := map[string]audit.Action{
		http.MethodGet:    audit.ActionRead,
		http.MethodPost:   audit.ActionUpdate,
		http.MethodPut:    audit.ActionUpdate,
		http.MethodDelete: audit.ActionDelete,
	}
	sink := &memSink{}
	a := audit.New(sink)
	for m := range cases {
		h := a.HTTPMiddleware(func(*http.Request) (audit.Resource, string, string) {
			return resUser, "x", ""
		})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(m, "/x", nil))
	}
	a.Stop() // flush
	logs := sink.snapshot()
	if len(logs) != len(cases) {
		t.Fatalf("want %d, got %d", len(cases), len(logs))
	}
	for _, l := range logs {
		if l.Action != cases[l.Method] {
			t.Fatalf("method %s -> action %d, want %d", l.Method, l.Action, cases[l.Method])
		}
	}
}

func TestAudit_WithUserID(t *testing.T) {
	ctx := audit.WithUserID(context.Background(), "alice")
	if audit.UserIDFromCtx(ctx) != "alice" {
		t.Fatal("uid not in ctx")
	}
}
