package tenant_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rushteam/beauty/pkg/api/metadata"
	"github.com/rushteam/beauty/pkg/middleware/tenant"
)

func TestHTTPMiddleware_ExtractsFromHeader(t *testing.T) {
	var got string
	handler := tenant.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = tenant.FromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "t1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got != "t1" {
		t.Fatalf("want tenant=t1, got %q", got)
	}
}

func TestHTTPMiddleware_ExtractsFromMetadataMD(t *testing.T) {
	var got string
	handler := tenant.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = tenant.FromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	md := metadata.New()
	md.Set(metadata.KeyTenantID, "t2")
	req = req.WithContext(metadata.NewContext(req.Context(), md))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got != "t2" {
		t.Fatalf("want tenant=t2, got %q", got)
	}
}

func TestHTTPMiddleware_RequiredRejects(t *testing.T) {
	handler := tenant.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestHTTPMiddleware_NotRequiredPasses(t *testing.T) {
	called := false
	handler := tenant.HTTPMiddleware(tenant.WithRequired(false))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if id := tenant.FromContext(r.Context()); id != "" {
			t.Fatalf("want empty tenant, got %q", id)
		}
	}))

	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("handler should be called when not required")
	}
}

func TestHTTPMiddleware_ValidatorRejects(t *testing.T) {
	handler := tenant.HTTPMiddleware(
		tenant.WithValidator(func(id string) bool { return id == "allowed" }),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for invalid tenant")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "blocked")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestHTTPMiddleware_ValidatorAllows(t *testing.T) {
	var got string
	handler := tenant.HTTPMiddleware(
		tenant.WithValidator(func(id string) bool { return id == "allowed" }),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = tenant.FromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-ID", "allowed")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got != "allowed" {
		t.Fatalf("want tenant=allowed, got %q", got)
	}
}

func TestHTTPMiddleware_CustomErrorHandler(t *testing.T) {
	handler := tenant.HTTPMiddleware(
		tenant.WithErrorHandler(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestHTTPMiddleware_CustomHeaderName(t *testing.T) {
	var got string
	handler := tenant.HTTPMiddleware(
		tenant.WithHeaderName("X-Org-ID"),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = tenant.FromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Org-ID", "org1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got != "org1" {
		t.Fatalf("want tenant=org1, got %q", got)
	}
}

func TestFromContext_EmptyWithout(t *testing.T) {
	if id := tenant.FromContext(context.Background()); id != "" {
		t.Fatalf("want empty, got %q", id)
	}
}

func TestNewContext_RoundTrip(t *testing.T) {
	ctx := tenant.NewContext(context.Background(), "t3")
	if id := tenant.FromContext(ctx); id != "t3" {
		t.Fatalf("want t3, got %q", id)
	}
}
