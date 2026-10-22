package httpcache_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/httpcache"
)

func TestPolicy_Header(t *testing.T) {
	if got := httpcache.NoStore.Header(); got != "no-store" {
		t.Fatalf("NoStore = %q", got)
	}
	p := httpcache.Policy{MaxAge: 30 * time.Second, SWR: time.Minute}
	got := p.Header()
	if !strings.Contains(got, "max-age=30") || !strings.Contains(got, "stale-while-revalidate=60") || !strings.Contains(got, "public") {
		t.Fatalf("got %q", got)
	}
}

func TestMiddleware_PathRules(t *testing.T) {
	mw := httpcache.Middleware(httpcache.NoStore,
		httpcache.Rule{Prefix: "/assets/", Policy: httpcache.HashedAsset},
		httpcache.Rule{Prefix: "/api/public/", Policy: httpcache.PublicShort},
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	cases := []struct {
		path   string
		substr string
	}{
		{"/api/private", "no-store"},
		{"/api/public/feed", "stale-while-revalidate"},
		{"/assets/app.abc.js", "immutable"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		cc := rec.Header().Get("Cache-Control")
		if !strings.Contains(cc, tc.substr) {
			t.Fatalf("path %s: Cache-Control=%q, want contain %q", tc.path, cc, tc.substr)
		}
	}
}

func TestMiddleware_RespectDownstream(t *testing.T) {
	mw := httpcache.Middleware(httpcache.NoStore)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=9")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Cache-Control"); got != "max-age=9" {
		t.Fatalf("got %q", got)
	}
}

func TestMiddleware_LongestPrefixWins(t *testing.T) {
	mw := httpcache.Middleware(httpcache.NoStore,
		httpcache.Rule{Prefix: "/api/", Policy: httpcache.PublicShort},
		httpcache.Rule{Prefix: "/api/admin/", Policy: httpcache.NoStore},
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/x", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("got %q, want no-store", got)
	}
}
