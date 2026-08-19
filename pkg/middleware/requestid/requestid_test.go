package requestid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPMiddleware_PreservesHeader(t *testing.T) {
	var ctxID string
	handler := HTTPMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxID = FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(Header, "custom-id")
	handler.ServeHTTP(rec, req)

	if ctxID != "custom-id" {
		t.Fatalf("context id = %q, want custom-id", ctxID)
	}
	if got := rec.Header().Get(Header); got != "custom-id" {
		t.Fatalf("response header = %q, want custom-id", got)
	}
}

func TestHTTPMiddleware_GeneratesID(t *testing.T) {
	var ctxID string
	handler := NewHTTPMiddleware(WithIDGenerator(func(context.Context) string {
		return "generated-id"
	}))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxID = FromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if ctxID != "generated-id" {
		t.Fatalf("context id = %q", ctxID)
	}
	if rec.Header().Get(Header) != "generated-id" {
		t.Fatalf("response header = %q", rec.Header().Get(Header))
	}
}

func TestFromContextAndNewContext(t *testing.T) {
	ctx := NewContext(context.Background(), "abc")
	if got := FromContext(ctx); got != "abc" {
		t.Fatalf("FromContext = %q", got)
	}
}
