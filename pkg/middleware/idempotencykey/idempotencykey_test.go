package idempotencykey_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/middleware/idempotencykey"
)

func TestMiddleware_Replay(t *testing.T) {
	var n atomic.Int32
	h := idempotencykey.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	do := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/orders", nil)
		req.Header.Set(idempotencykey.Header, "key-1")
		h.ServeHTTP(rec, req)
		return rec
	}

	r1 := do()
	r2 := do()
	if n.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", n.Load())
	}
	if r1.Code != http.StatusCreated || r2.Code != http.StatusCreated {
		t.Fatalf("status %d / %d", r1.Code, r2.Code)
	}
	if r1.Header().Get(idempotencykey.ReplayHeader) != "" {
		t.Fatal("first response should not be marked replayed")
	}
	if r2.Header().Get(idempotencykey.ReplayHeader) != "true" {
		t.Fatal("second response should be marked replayed")
	}
	if r1.Body.String() != r2.Body.String() {
		t.Fatalf("body mismatch %q vs %q", r1.Body.String(), r2.Body.String())
	}
}

func TestMiddleware_RequireKey(t *testing.T) {
	h := idempotencykey.Middleware(idempotencykey.WithRequireKey(true))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestMiddleware_GetPassthrough(t *testing.T) {
	var n atomic.Int32
	h := idempotencykey.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	for range 2 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(idempotencykey.Header, "k")
		h.ServeHTTP(rec, req)
	}
	if n.Load() != 2 {
		t.Fatalf("GET should not be idempotent-cached, calls=%d", n.Load())
	}
}

func TestMiddleware_5xxNotCached(t *testing.T) {
	var n atomic.Int32
	h := idempotencykey.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("err"))
	}))
	do := func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set(idempotencykey.Header, "fail-key")
		h.ServeHTTP(rec, req)
	}
	do()
	do()
	if n.Load() != 2 {
		t.Fatalf("5xx should not cache, calls=%d", n.Load())
	}
}
