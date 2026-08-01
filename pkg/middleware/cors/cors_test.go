package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(c *Config, origin string) http.Header {
	h := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Header()
}

// 通配符 + credentials：不安全的组合，应禁用 credentials 并回退为 "*"。
func TestCORS_WildcardWithCredentials_DisablesCredentials(t *testing.T) {
	c := Default()
	c.AllowCredentials = true // AllowOrigins 默认 ["*"]

	got := serve(c, "https://app.example.com")

	if ao := got.Get("Access-Control-Allow-Origin"); ao != "*" {
		t.Fatalf("want *, got %q", ao)
	}
	if got.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials header must NOT be set with wildcard origins")
	}
}

// 通配符 + 不带 credentials：发 "*"，且不发 credentials 头。
func TestCORS_WildcardNoCredentials(t *testing.T) {
	c := Default() // AllowCredentials=false

	got := serve(c, "https://app.example.com")

	if ao := got.Get("Access-Control-Allow-Origin"); ao != "*" {
		t.Fatalf("want *, got %q", ao)
	}
	if got.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials header must NOT be set with wildcard")
	}
}
