package etag_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rushteam/beauty/pkg/middleware/etag"
)

func handler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
}

func TestETag_SetsHeaderAndBody(t *testing.T) {
	h := etag.Middleware(handler("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("应设置 ETag 头")
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestETag_NotModified(t *testing.T) {
	h := etag.Middleware(handler("hello"))

	// 先取一次拿到 ETag。
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	tag := rec1.Header().Get("ETag")

	// 带 If-None-Match 再取,应 304 且无体。
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", tag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusNotModified {
		t.Fatalf("应 304, got %d", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Fatalf("304 不应有响应体, got %q", rec2.Body.String())
	}
	if rec2.Header().Get("ETag") != tag {
		t.Fatal("304 应保留 ETag 头")
	}
}

func TestETag_ContentChangesTag(t *testing.T) {
	tagOf := func(body string) string {
		rec := httptest.NewRecorder()
		etag.Middleware(handler(body)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Header().Get("ETag")
	}
	if tagOf("a") == tagOf("b") {
		t.Fatal("不同内容应产生不同 ETag")
	}
}

func TestETag_WildcardMatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", "*")
	rec := httptest.NewRecorder()
	etag.Middleware(handler("x")).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: * 应 304, got %d", rec.Code)
	}
}

func TestETag_NonGETPassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	etag.Middleware(handler("x")).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Header().Get("ETag") != "" {
		t.Fatal("非 GET 不应设置 ETag")
	}
	if rec.Body.String() != "x" {
		t.Fatalf("非 GET 应原样透传, body=%q", rec.Body.String())
	}
}

func TestETag_RespectsDownstreamETag(t *testing.T) {
	h := etag.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"custom"`)
		_, _ = io.WriteString(w, "x")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("ETag") != `"custom"` {
		t.Fatalf("应尊重下游 ETag, got %q", rec.Header().Get("ETag"))
	}
}
