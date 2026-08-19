package antireplay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rushteam/beauty/pkg/store/kvstore"
)

func ok200(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestMissingNonce(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()

	h := HTTPMiddleware(store)(http.HandlerFunc(ok200))
	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestUniqueNonce(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()

	h := HTTPMiddleware(store)(http.HandlerFunc(ok200))
	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	req.Header.Set("X-Nonce", "abc123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rec.Code)
	}
}

func TestReplayDetected(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()

	h := HTTPMiddleware(store)(http.HandlerFunc(ok200))

	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	req.Header.Set("X-Nonce", "dup-nonce")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first: got %d, want 200", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	req2.Header.Set("X-Nonce", "dup-nonce")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("replay: got %d, want 403", rec2.Code)
	}
}

func TestSkipPrefix(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()

	h := HTTPMiddleware(store, WithSkipPrefixes("/healthz", "/callback/"))(http.HandlerFunc(ok200))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip /healthz: got %d, want 200", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/callback/wechat", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("skip /callback/: got %d, want 200", rec2.Code)
	}
}

func TestCustomHeader(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()

	h := HTTPMiddleware(store, WithHeader("X-Request-Nonce"))(http.HandlerFunc(ok200))

	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	req.Header.Set("X-Request-Nonce", "n1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("custom header: got %d, want 200", rec.Code)
	}
}
