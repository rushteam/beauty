package wasm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/wasm"
)

// buildFaaSGuest 复用中间件 guest 的 alloc/handle ABI,response 为 Response JSON。
func buildFaaSGuest(response []byte) []byte {
	return buildMiddleware(response)
}

func TestHandler_BasicResponse(t *testing.T) {
	resp := `{"status":200,"headers":{"Content-Type":"application/json"},"body":"{\"msg\":\"hello\"}"}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))
	if err != nil {
		t.Fatal(err)
	}

	h := wasm.Handler(mod)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/greet", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("want Content-Type=application/json, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hello") {
		t.Fatalf("want body contains 'hello', got %q", body)
	}
}

func TestHandler_CustomStatus(t *testing.T) {
	resp := `{"status":201,"body":"created"}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))
	if err != nil {
		t.Fatal(err)
	}
	h := wasm.Handler(mod)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != 201 {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	if rec.Body.String() != "created" {
		t.Fatalf("want 'created', got %q", rec.Body.String())
	}
}

func TestHandler_DefaultStatusOK(t *testing.T) {
	resp := `{"body":"ok"}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))
	if err != nil {
		t.Fatal(err)
	}
	h := wasm.Handler(mod)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status 0 should default to 200, got %d", rec.Code)
	}
}

func TestHandler_WithPool(t *testing.T) {
	resp := `{"status":200,"body":"pooled"}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))
	if err != nil {
		t.Fatal(err)
	}
	h := wasm.Handler(mod, wasm.WithHandlerPool(4))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("pooled: want 200, got %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}

func TestHandler_Timeout(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildSpinGuest())
	if err != nil {
		t.Fatal(err)
	}
	h := wasm.Handler(mod, wasm.WithHandlerTimeout(50*time.Millisecond))

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("timeout should 500, got %d", rec.Code)
	}
	if elapsed > time.Second {
		t.Fatalf("should return near timeout, took %s", elapsed)
	}
}

func TestHandler_WithBody(t *testing.T) {
	resp := `{"status":200,"body":"got-body"}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))
	if err != nil {
		t.Fatal(err)
	}
	h := wasm.Handler(mod, wasm.WithHandlerBody(1024))

	body := strings.NewReader("request-payload")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

// ===== Router 测试 =====

func TestRouter_BasicDispatch(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	greetResp := `{"status":200,"body":"hello"}`
	echoResp := `{"status":200,"body":"echo"}`

	greetMod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(greetResp)))
	echoMod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(echoResp)))

	router := wasm.NewRouter(rt)
	router.Register("/greet", greetMod)
	router.Register("/echo", echoMod)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/greet", nil))
	if rec.Body.String() != "hello" {
		t.Fatalf("/greet: want 'hello', got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/echo", nil))
	if rec.Body.String() != "echo" {
		t.Fatalf("/echo: want 'echo', got %q", rec.Body.String())
	}
}

func TestRouter_NotFound(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	router := wasm.NewRouter(rt)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unregistered path should 404, got %d", rec.Code)
	}
}

func TestRouter_PrefixMatch(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	resp := `{"status":200,"body":"prefix"}`
	mod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))

	router := wasm.NewRouter(rt)
	router.Register("/api/", mod) // prefix pattern

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if rec.Body.String() != "prefix" {
		t.Fatalf("prefix match should work, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-prefix should 404, got %d", rec.Code)
	}
}

func TestRouter_HotReplace(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	v1 := `{"status":200,"body":"v1"}`
	v2 := `{"status":200,"body":"v2"}`
	mod1, _ := rt.Compile(ctx, buildFaaSGuest([]byte(v1)))
	mod2, _ := rt.Compile(ctx, buildFaaSGuest([]byte(v2)))

	router := wasm.NewRouter(rt)
	router.Register("/fn", mod1)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fn", nil))
	if rec.Body.String() != "v1" {
		t.Fatalf("before replace: want 'v1', got %q", rec.Body.String())
	}

	router.Register("/fn", mod2) // hot replace

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fn", nil))
	if rec.Body.String() != "v2" {
		t.Fatalf("after replace: want 'v2', got %q", rec.Body.String())
	}
}

func TestRouter_Deregister(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	resp := `{"status":200,"body":"bye"}`
	mod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))

	router := wasm.NewRouter(rt)
	router.Register("/fn", mod)
	router.Deregister("/fn")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fn", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deregistered should 404, got %d", rec.Code)
	}
}

func TestRouter_RegisterBytes(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	resp := `{"status":200,"body":"from-bytes"}`
	router := wasm.NewRouter(rt)
	if err := router.RegisterBytes(ctx, "/fn", buildFaaSGuest([]byte(resp))); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fn", nil))
	if rec.Body.String() != "from-bytes" {
		t.Fatalf("want 'from-bytes', got %q", rec.Body.String())
	}
}

func TestRouter_Patterns(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	resp := `{"status":200,"body":"x"}`
	mod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))

	router := wasm.NewRouter(rt)
	router.Register("/a", mod)
	router.Register("/b", mod)

	patterns := router.Patterns()
	if len(patterns) != 2 {
		t.Fatalf("want 2 patterns, got %d", len(patterns))
	}
}

func TestRouter_Concurrent(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	resp := `{"status":200,"body":"ok"}`
	mod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))

	router := wasm.NewRouter(rt)
	router.Register("/fn", mod)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fn", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("concurrent: want 200, got %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}

// TestRouter_ExactOverPrefix 精确匹配优先于前缀匹配。
func TestRouter_ExactOverPrefix(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	prefixResp := `{"status":200,"body":"prefix"}`
	exactResp := `{"status":200,"body":"exact"}`
	prefixMod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(prefixResp)))
	exactMod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(exactResp)))

	router := wasm.NewRouter(rt)
	router.Register("/api/", prefixMod)
	router.Register("/api/special", exactMod)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/special", nil))
	if rec.Body.String() != "exact" {
		t.Fatalf("exact should win over prefix, got %q", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/other", nil))
	if rec.Body.String() != "prefix" {
		t.Fatalf("prefix should catch others, got %q", rec.Body.String())
	}
}
