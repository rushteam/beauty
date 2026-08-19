package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestNotify_DefaultJSONBody(t *testing.T) {
	var got string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = string(b)
		mu.Unlock()
	}))
	defer srv.Close()

	n := New()
	if err := n.Add(Endpoint{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	n.Notify(context.Background(), Event{Type: "t", Payload: map[string]string{"k": "v"}})

	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return got != "" })
	if !strings.Contains(got, `"k":"v"`) {
		t.Fatalf("body = %s", got)
	}
}

func TestNotify_EventFilter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	n := New()
	_ = n.Add(Endpoint{URL: srv.URL, Events: []string{"order.paid"}})

	n.Notify(context.Background(), Event{Type: "order.created"}) // 不匹配
	n.Notify(context.Background(), Event{Type: "order.paid"})    // 匹配
	waitFor(t, func() bool { return hits.Load() == 1 })
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 1 {
		t.Fatalf("only matching event should fire, got %d", hits.Load())
	}
}

func TestNotify_HMACSignature(t *testing.T) {
	secret := "s3cr3t"
	var sigOK atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		sigOK.Store(r.Header.Get("X-Webhook-Signature") == want)
	}))
	defer srv.Close()

	n := New()
	_ = n.Add(Endpoint{URL: srv.URL, Secret: secret})
	n.Notify(context.Background(), Event{Type: "t", Payload: 123})
	waitFor(t, func() bool { return sigOK.Load() })
}

func TestNotify_BodyTemplate(t *testing.T) {
	var got string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = string(b)
		mu.Unlock()
	}))
	defer srv.Close()

	n := New()
	if err := n.Add(Endpoint{URL: srv.URL, BodyTemplate: `event={{.Type}}`}); err != nil {
		t.Fatal(err)
	}
	n.Notify(context.Background(), Event{Type: "ping"})
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return got != "" })
	if got != "event=ping" {
		t.Fatalf("template body = %q", got)
	}
}

func TestNotify_RetryThenError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError) // 一直失败
	}))
	defer srv.Close()

	var gotErr atomic.Bool
	n := New(WithRetries(2), WithBackoff(time.Millisecond),
		WithErrorHandler(func(_ Endpoint, _ Event, err error) { gotErr.Store(err != nil) }))
	_ = n.Add(Endpoint{URL: srv.URL})
	n.Notify(context.Background(), Event{Type: "t"})

	waitFor(t, func() bool { return gotErr.Load() })
	if calls.Load() != 3 { // 1 + 2 retries
		t.Fatalf("want 3 attempts, got %d", calls.Load())
	}
}

func TestAdd_BadTemplate(t *testing.T) {
	if err := New().Add(Endpoint{URL: "http://x", BodyTemplate: "{{.Unclosed"}); err == nil {
		t.Fatal("want error for bad template")
	}
}

func TestNotify_Dedup_SameEventID(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	store := NewMemStore()
	n := New(WithStore(store))
	_ = n.Add(Endpoint{URL: srv.URL})

	// 同一 EventID 投递两次:应只命中一次。
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-1", Payload: "x"})
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-1", Payload: "x"})
	waitFor(t, func() bool { return hits.Load() == 1 })
	time.Sleep(100 * time.Millisecond)
	if hits.Load() != 1 {
		t.Fatalf("dedup: want 1 hit, got %d", hits.Load())
	}
}

func TestNotify_Dedup_DifferentEventID(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	store := NewMemStore()
	n := New(WithStore(store))
	_ = n.Add(Endpoint{URL: srv.URL})

	// 不同 EventID:都应命中。
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-a"})
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-b"})
	waitFor(t, func() bool { return hits.Load() == 2 })
}

func TestNotify_Dedup_PerEndpoint(t *testing.T) {
	// 同一 EventID 投给两个不同 endpoint:两个都应命中(去重按 endpoint 维度)。
	var hits atomic.Int32
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv1.Close()
	defer srv2.Close()

	store := NewMemStore()
	n := New(WithStore(store))
	_ = n.Add(Endpoint{URL: srv1.URL})
	_ = n.Add(Endpoint{URL: srv2.URL})

	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-shared"})
	waitFor(t, func() bool { return hits.Load() == 2 })
}

func TestNotify_DeliveryRecord_Tracked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	store := NewMemStore()
	n := New(WithStore(store))
	_ = n.Add(Endpoint{URL: srv.URL})
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-1"})
	waitFor(t, func() bool { return len(store.Records()) == 1 })
	recs := store.Records()
	if recs[0].Status != StatusDelivered {
		t.Fatalf("want delivered, got %s", recs[0].Status)
	}
	if recs[0].EndpointURL != srv.URL {
		t.Fatalf("wrong endpoint: %s", recs[0].EndpointURL)
	}
}

func TestNotify_DLQ_OnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	dlq := NewMemDLQ()
	store := NewMemStore()
	var gotErr atomic.Bool
	n := New(WithRetries(1), WithBackoff(time.Millisecond),
		WithStore(store), WithDLQ(dlq),
		WithErrorHandler(func(_ Endpoint, _ Event, err error) { gotErr.Store(err != nil) }))
	_ = n.Add(Endpoint{URL: srv.URL})
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-fail"})

	waitFor(t, func() bool { return dlq.Len() == 1 })
	if !gotErr.Load() {
		t.Fatal("error handler should fire")
	}
	rec, ok := dlq.Pop()
	if !ok || rec.Status != StatusFailed || rec.EventID != "evt-fail" {
		t.Fatalf("dlq record: %+v ok=%v", rec, ok)
	}
	// 失败也应被 store 记录。
	waitFor(t, func() bool {
		for _, r := range store.Records() {
			if r.Status == StatusFailed {
				return true
			}
		}
		return false
	})
}

func TestNotify_NoEventID_NoDedup(t *testing.T) {
	// EventID 为空:不去重,每次都投递。
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	store := NewMemStore()
	n := New(WithStore(store))
	_ = n.Add(Endpoint{URL: srv.URL})
	n.Notify(context.Background(), Event{Type: "t"}) // 无 EventID
	n.Notify(context.Background(), Event{Type: "t"})
	waitFor(t, func() bool { return hits.Load() == 2 })
}

func TestReplay_Redelivers(t *testing.T) {
	failCount := atomic.Int32{}
	var srvHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srvHits.Add(1)
		if failCount.Add(1) <= 1 { // 第一次失败,第二次(Replay)成功
			w.WriteHeader(500)
			return
		}
	}))
	defer srv.Close()

	dlq := NewMemDLQ()
	n := New(WithRetries(0), WithBackoff(time.Millisecond), WithDLQ(dlq))
	_ = n.Add(Endpoint{URL: srv.URL})
	n.Notify(context.Background(), Event{Type: "t", EventID: "evt-x"})
	waitFor(t, func() bool { return dlq.Len() == 1 })

	// Replay:应重新投递(这次成功)。
	ok, err := n.Replay(context.Background())
	if !ok {
		t.Fatal("replay should pop a record")
	}
	if err != nil {
		t.Fatalf("replay should succeed, got %v", err)
	}
	finalHits := srvHits.Load()
	if finalHits < 2 { // 失败1次 + replay成功1次
		t.Fatalf("replay should hit server again, got %d", finalHits)
	}
	if dlq.Len() != 0 {
		t.Fatalf("dlq should be empty after successful replay, got %d", dlq.Len())
	}
}

func TestReplay_NoDLQ(t *testing.T) {
	n := New()
	ok, err := n.Replay(context.Background())
	if ok || err != nil {
		t.Fatalf("no DLQ: want (false,nil), got (%v,%v)", ok, err)
	}
}

func TestMemStore_Records_Snapshot(t *testing.T) {
	s := NewMemStore()
	s.RecordDelivered(DeliveryRecord{EventID: "a", Status: StatusDelivered})
	s.RecordFailed(DeliveryRecord{EventID: "b", Status: StatusFailed})
	recs := s.Records()
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	// 修改快照不影响内部。
	recs[0].EventID = "mutated"
	recs2 := s.Records()
	if recs2[0].EventID != "a" {
		t.Fatal("snapshot should be a copy")
	}
}

func TestMemDLQ_FIFO(t *testing.T) {
	q := NewMemDLQ()
	q.Push(DeliveryRecord{EventID: "first"})
	q.Push(DeliveryRecord{EventID: "second"})
	if q.Len() != 2 {
		t.Fatalf("len=%d want 2", q.Len())
	}
	r1, ok := q.Pop()
	if !ok || r1.EventID != "first" {
		t.Fatalf("pop1: %+v ok=%v", r1, ok)
	}
	r2, ok := q.Pop()
	if !ok || r2.EventID != "second" {
		t.Fatalf("pop2: %+v ok=%v", r2, ok)
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("pop on empty should return false")
	}
}
