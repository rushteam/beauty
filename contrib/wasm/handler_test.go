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

func TestHandler_Observer(t *testing.T) {
	resp := `{"status":201,"body":"ok"}`
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

	var ev wasm.HandlerEvent
	h := wasm.Handler(mod, wasm.WithHandlerObserver(func(e wasm.HandlerEvent) { ev = e }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if ev.Status != 201 {
		t.Fatalf("observer status: want 201, got %d", ev.Status)
	}
	if ev.Err != nil {
		t.Fatalf("observer err: %v", ev.Err)
	}
	if ev.Duration <= 0 {
		t.Fatalf("observer duration should be >0")
	}
}

func TestHandler_WithWarm(t *testing.T) {
	resp := `{"status":200,"body":"warm"}`
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
	h := wasm.Handler(mod, wasm.WithHandlerPool(4), wasm.WithHandlerWarm(2))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "warm" {
		t.Fatalf("warm handler: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestRouter_Stats(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	resp := `{"status":200,"body":"ok"}`
	mod, _ := rt.Compile(ctx, buildFaaSGuest([]byte(resp)))

	router := wasm.NewRouter(rt)
	router.Register("/a", mod)
	router.Register("/b", mod)

	st := router.Stats()
	if st.Functions != 2 {
		t.Fatalf("Functions: want 2, got %d", st.Functions)
	}

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/a", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/b", nil))

	st = router.Stats()
	if st.Hits != 2 {
		t.Fatalf("Hits: want 2, got %d", st.Hits)
	}
	if st.Misses != 1 {
		t.Fatalf("Misses: want 1, got %d", st.Misses)
	}
}

func TestPool_WarmAndIdle(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildAdd())
	if err != nil {
		t.Fatal(err)
	}
	pool := mod.NewPool(4)
	if err := pool.Warm(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if got := pool.Idle(); got != 3 {
		t.Fatalf("Idle after warm: want 3, got %d", got)
	}
	inst, err := pool.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.Idle(); got != 2 {
		t.Fatalf("Idle after Get: want 2, got %d", got)
	}
	pool.Put(ctx, inst)
	if got := pool.Idle(); got != 3 {
		t.Fatalf("Idle after Put: want 3, got %d", got)
	}
	pool.Close(ctx)
}

func TestRuntime_WithCacheDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rt1, err := wasm.New(ctx, wasm.WithCacheDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	mod1, err := rt1.Compile(ctx, buildAdd())
	if err != nil {
		t.Fatal(err)
	}
	inst1, err := mod1.Instantiate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := inst1.Call(ctx, "add", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if res[0] != 5 {
		t.Fatalf("add: want 5, got %d", res[0])
	}
	_ = inst1.Close(ctx)
	_ = rt1.Close(ctx)

	// 新 Runtime 复用同一缓存目录,应能编译并调用
	rt2, err := wasm.New(ctx, wasm.WithCacheDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.Close(ctx)
	mod2, err := rt2.Compile(ctx, buildAdd())
	if err != nil {
		t.Fatal(err)
	}
	inst2, err := mod2.Instantiate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer inst2.Close(ctx)
	res, err = inst2.Call(ctx, "add", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if res[0] != 30 {
		t.Fatalf("cached add: want 30, got %d", res[0])
	}
}
