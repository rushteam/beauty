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
