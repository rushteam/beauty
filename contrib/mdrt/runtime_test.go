package mdrt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/pkg/security"
)

func TestFixture(t *testing.T) {
	rt, srv := startDir(t, "example/specs", ModeOffline, nil)
	defer srv.Close()
	for _, st := range rt.Status() {
		t.Logf("status %s %s %v", st.Slug, st.State, st.Diagnostics)
	}
	tok := token(t, "test-secret", "u1")

	res := do(t, srv, "POST", "/orders", tok, `{"amount":1200}`)
	if res.StatusCode != 201 || !strings.Contains(readAll(t, res), `"pay":1080`) {
		t.Fatalf("下单: %d %s", res.StatusCode, "")
	}
	res = do(t, srv, "POST", "/orders", tok, `{"amount":0}`)
	if res.StatusCode != 400 {
		t.Fatalf("非法金额 %d", res.StatusCode)
	}
	res = do(t, srv, "POST", "/orders", "", `{"amount":10}`)
	if res.StatusCode != 401 {
		t.Fatalf("未登录 %d", res.StatusCode)
	}
	res = do(t, srv, "GET", "/orders/o1", tok, "")
	body := readAll(t, res)
	if res.StatusCode != 200 || !strings.Contains(body, `"id":"o1"`) {
		t.Fatalf("查询 %d %s", res.StatusCode, body)
	}
	_ = rt
}

func TestBadFileDoesNotBlockOthers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ping.md"), pingSpec("ping", "pong"))
	writeFile(t, filepath.Join(dir, "pong.md"), pingSpec("pong", "pong"))
	_, srv := startDir(t, dir, ModeOffline, nil)
	defer srv.Close()

	writeFile(t, filepath.Join(dir, "ping.md"), "这不是 markdown 规格\n")
	writeFile(t, filepath.Join(dir, "pong.md"), pingSpec("pong", "changed"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		res := do(t, srv, "GET", "/pong", "", "")
		body := readAll(t, res)
		if strings.Contains(body, "changed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pong 没有更新: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	res := do(t, srv, "GET", "/ping", "", "")
	body := readAll(t, res)
	if !strings.Contains(body, "pong") || strings.Contains(body, "changed") {
		t.Fatalf("坏文件盖掉了旧实现: %s", body)
	}
}

func TestReloadKeepsStore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "box.md"), boxSpec("v1"))
	_, srv := startDir(t, dir, ModeOffline, nil)
	defer srv.Close()
	res := do(t, srv, "POST", "/box", "", "")
	if res.StatusCode != 204 {
		t.Fatalf("写入 %d", res.StatusCode)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				res := do(t, srv, "GET", "/box", "", "")
				body := readAll(t, res)
				if res.StatusCode != 200 || !strings.Contains(body, `"n":7`) {
					t.Errorf("热切换期间 store 丢了: %d %s", res.StatusCode, body)
					return
				}
			}
		}
	}()
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(dir, "box.md"), boxSpec("v1"+"/*"+strings.Repeat(".", i)+"*/"))
		time.Sleep(40 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

func TestMechanicalLatency(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ping.md"), pingSpec("ping", "a"))
	_, srv := startDir(t, dir, ModeOffline, nil)
	defer srv.Close()
	var worst time.Duration
	for i, msg := range []string{"b", "c", "d", "e"} {
		writeFile(t, filepath.Join(dir, "ping.md"), pingSpec("ping", msg))
		start := time.Now()
		deadline := start.Add(500 * time.Millisecond)
		seen := false
		for time.Now().Before(deadline) {
			res := do(t, srv, "GET", "/ping", "", "")
			body := readAll(t, res)
			if strings.Contains(body, msg) {
				seen = true
				if d := time.Since(start); d > worst {
					worst = d
				}
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !seen {
			t.Fatalf("第 %d 次保存超过 500ms 还没生效", i)
		}
	}
	t.Logf("最慢一次 %s", worst)
}

func TestCacheSkipsLLM(t *testing.T) {
	dir := t.TempDir()
	src := behaviorPing("ping", "返回 pong")
	writeFile(t, filepath.Join(dir, "ping.md"), src)
	state := t.TempDir()
	llm := &scriptedLLM{bodies: []string{`return { status: 200, body: { msg: "pong" } };`}}
	rt := newRT(t, dir, state, ModeDev, llm)
	spec, diags := Parse("ping.md", src)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	key := CacheKey("test", spec.Units[0])
	if err := rt.cache.Put(key, cacheEntry{Body: `return { status: 200, body: { msg: "pong" } };`, Model: "test"}); err != nil {
		t.Fatal(err)
	}
	res := rt.translate(context.Background(), filepath.Join(dir, "ping.md"), []byte(src), produceFlags{sync: true})
	if res.js == "" || llm.n != 0 {
		t.Fatalf("不应调用模型 js=%q calls=%d diags=%v", res.js, llm.n, res.diags)
	}
	src2 := behaviorPing("ping", "返回 pong")
	src2 = strings.Replace(src2, "### 正常", "### 正常请求", 1)
	res = rt.translate(context.Background(), filepath.Join(dir, "ping.md"), []byte(src2), produceFlags{sync: true})
	if llm.n != 0 {
		t.Fatalf("只改例子标题仍调用了模型 %d 次", llm.n)
	}
}

func TestStaticRepair(t *testing.T) {
	dir := t.TempDir()
	src := behaviorPing("ping", "返回 pong")
	llm := &scriptedLLM{bodies: []string{
		`$http.send({url:"http://evil"});`,
		`return { status: 200, body: { msg: "pong" } };`,
	}}
	rt := newRT(t, dir, t.TempDir(), ModeDev, llm)
	res := rt.translate(context.Background(), filepath.Join(dir, "ping.md"), []byte(src), produceFlags{sync: true})
	if res.js == "" {
		t.Fatal(res.diags)
	}
	if llm.n < 2 {
		t.Fatalf("修复轮次 %d", llm.n)
	}
}

func TestBuildOfflineRequiresCache(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ping.md"), behaviorPing("ping", "返回 pong"))
	state := t.TempDir()
	if _, err := Build(context.Background(),
		WithSpecDir(dir),
		WithStateDir(state),
		WithLockFile(filepath.Join(state, "lock.json")),
		WithModel("test"),
		WithJWTSecret("test-secret"),
	); err == nil {
		t.Fatal("缺缓存时应失败")
	}
	rt := newRT(t, dir, state, ModeOffline, nil)
	spec, diags := Parse("ping.md", behaviorPing("ping", "返回 pong"))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if err := rt.cache.Put(CacheKey("test", spec.Units[0]), cacheEntry{
		Body: `return { status: 200, body: { msg: "pong" } };`, Model: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(),
		WithSpecDir(dir),
		WithStateDir(state),
		WithLockFile(filepath.Join(state, "lock.json")),
		WithModel("test"),
		WithJWTSecret("test-secret"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "gen", "ping.js")); err != nil {
		t.Fatal(err)
	}
}

func TestAllowTransport(t *testing.T) {
	var allow atomic.Pointer[hostAllow]
	allow.Store(&hostAllow{hosts: map[string]struct{}{"api.example.com": {}}})
	tr := allowTransport{
		base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: make(http.Header), Request: r}, nil
		}),
		allow: &allow,
	}
	req, _ := http.NewRequest(http.MethodGet, "https://evil.example/x", nil)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("未声明的域名应被拒绝")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://api.example.com/x", nil)
	res, err := tr.RoundTrip(req)
	if err != nil || res.StatusCode != 204 {
		t.Fatalf("放行失败 %v", err)
	}
}

type scriptedLLM struct {
	bodies []string
	n      int
}

func (s *scriptedLLM) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.n >= len(s.bodies) {
		return nil, fmt.Errorf("scripted llm: 没有更多输出")
	}
	body := s.bodies[s.n]
	s.n++
	raw, _ := json.Marshal(behaviorOut{Body: body})
	return &llm.Response{Content: string(raw), Usage: llm.Usage{InputTokens: 10, OutputTokens: 20}}, nil
}

func (s *scriptedLLM) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(func(llm.Chunk, error) bool) {}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func startDir(t *testing.T, dir string, mode Mode, client *scriptedLLM) (*Runtime, *httptest.Server) {
	t.Helper()
	rt := newRT(t, dir, t.TempDir(), mode, client)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- rt.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rt.live.Load() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rt.live.Load() == nil {
		t.Fatal("运行时没有起来")
	}
	return rt, httptest.NewServer(rt)
}

func newRT(t *testing.T, specDir, state string, mode Mode, client *scriptedLLM) *Runtime {
	t.Helper()
	opts := []Option{
		WithSpecDir(specDir),
		WithStateDir(state),
		WithLockFile(filepath.Join(state, "lock.json")),
		WithMode(mode),
		WithJWTSecret("test-secret"),
		WithDebounce(20 * time.Millisecond),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithModel("test"),
	}
	if client != nil {
		opts = append(opts, WithLLM(client, "test"))
	}
	rt, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func pingSpec(name, msg string) string {
	return "---\ndialect: mdrt/v0\nname: " + name + "\nauth: none\n---\n\n# GET /" + name + "\n\n## 实现\n```js\nreturn { status: 200, body: { msg: " + strconvQuote(msg) + " } };\n```\n"
}

func boxSpec(note string) string {
	return "---\ndialect: mdrt/v0\nname: box\nauth: none\n---\n\n# POST /box\n\n## 实现\n```js\nctx.log.info(" + strconvQuote(note) + ");\nctx.store.set(\"n\", 7);\nreturn { status: 204 };\n```\n\n# GET /box\n\n## 实现\n```js\nreturn { status: 200, body: { n: ctx.store.get(\"n\") } };\n```\n"
}

func behaviorPing(name, behavior string) string {
	return "---\ndialect: mdrt/v0\nname: " + name + "\nauth: none\n---\n\n# GET /" + name + "\n\n## 行为\n" + behavior + "\n\n## 例子\n\n### 正常\n```req\nGET /" + name + "\n```\n```res\n200\n{\"msg\":\"pong\"}\n```\n\n### 边界\n```req\nGET /" + name + "\n```\n```res\n200\n{\"msg\":\"pong\"}\n```\n"
}

func strconvQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func token(t *testing.T, secret, uid string) string {
	t.Helper()
	tok, err := security.CreateJWT(map[string]any{"sub": uid}, []byte(secret), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func do(t *testing.T, srv *httptest.Server, method, path, bearer, body string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
