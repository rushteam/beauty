package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTemplatePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/users/123", "/users/{id}"},
		{"/users/123/orders/456", "/users/{id}/orders/{id}"},
		{"/users", "/users"},
		{"/", "/"},
		{"", "/"},
		{"/health", "/health"},
		{"/files/550e8400-e29b-41d4-a716-446655440000", "/files/{uuid}"},
		{"/blobs/0123456789abcdef0123456789abcdef", "/blobs/{hash}"},
		{"/v1/users/42/profile", "/v1/users/{id}/profile"},
		// 长 base62/snowflake 风格 ID
		{"/posts/1Az9Qx7Lm3Kp0Rt5Yw8Nb", "/posts/{id}"},
		// 纯静态长段不应被模板化
		{"/documentation/getting-started", "/documentation/getting-started"},
	}
	for _, c := range cases {
		if got := templatePath(c.in); got != c.want {
			t.Errorf("templatePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTemplatePathBounds(t *testing.T) {
	// 段数过多 → 归并
	deep := "/a/b/c/d/e/f/g/h/i/j/k/l/m/n"
	if got := templatePath(deep); got != "/other" {
		t.Errorf("templatePath(deep) = %q, want /other", got)
	}
}

func TestPatternPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"GET /users/{id}", "/users/{id}"},
		{"/users/{id}", "/users/{id}"},
		{"POST example.com/users/{id}", "/users/{id}"},
		{"example.com/items", "/items"},
		{"/", "/"},
	}
	for _, c := range cases {
		if got := patternPath(c.in); got != c.want {
			t.Errorf("patternPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 验证标准库 ServeMux 路由后 r.Pattern 被填充，且本中间件在 next 返回后能读到。
func TestResolveRouteWithServeMux(t *testing.T) {
	var captured string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {})
	// 模拟中间件：next 返回后解析 route
	handler := func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		captured = resolveRoute(r, nil)
	}
	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	handler(httptest.NewRecorder(), req)
	if captured != "/users/{id}" {
		t.Errorf("resolveRoute via ServeMux = %q, want /users/{id}", captured)
	}
}

func TestResolveRouteExtractorWins(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/123", nil)
	got := resolveRoute(req, func(*http.Request) string { return "/custom/{x}" })
	if got != "/custom/{x}" {
		t.Errorf("resolveRoute with extractor = %q, want /custom/{x}", got)
	}
	// extractor 返回空 → 回退到启发式
	got = resolveRoute(req, func(*http.Request) string { return "" })
	if got != "/users/{id}" {
		t.Errorf("resolveRoute extractor empty fallback = %q, want /users/{id}", got)
	}
}
