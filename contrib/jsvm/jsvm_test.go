package jsvm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/jsvm"
	"github.com/rushteam/beauty/pkg/messaging/eventbus"
	"github.com/rushteam/beauty/pkg/store/blob"
)

func newVM(t *testing.T, script string, opts ...jsvm.Option) *jsvm.VM {
	t.Helper()
	vm, err := jsvm.New(append([]jsvm.Option{jsvm.WithScript("test.js", script), jsvm.WithPoolSize(2)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(vm.Close)
	return vm
}

func do(t *testing.T, h http.Handler, method, target, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, r))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestRouter(t *testing.T) {
	vm := newVM(t, `
		routerUse((e) => { e.set("trace", "g"); e.setHeader("X-Global", "1"); return e.next(); });
		const auth = (e) => {
			if (e.request.header("Authorization") !== "Bearer ok") throw new UnauthorizedError("no token");
			e.set("user", "alice");
			return e.next();
		};
		routerAdd("GET", "/users/{id}", (e) => {
			e.json(200, { id: e.request.pathValue("id"), q: e.request.query("q"), trace: e.get("trace") });
		});
		routerAdd("POST", "/echo", (e) => {
			const body = e.request.json();
			e.json(201, { got: body.name, user: e.get("user") });
		}, auth);
		routerAdd("GET", "/bad", () => { throw new BadRequestError("bad input", { field: "x" }); });
		routerAdd("GET", "/boom", () => { throw new Error("secret detail"); });
		routerAdd("*", "/any", (e) => e.string(200, e.request.method));
		routerAdd("GET", "/twice", (e) => { e.noContent(204); e.noContent(204); });
	`)

	code, body := do(t, vm, "GET", "/users/42?q=hi", "")
	if code != 200 || body != `{"id":"42","q":"hi","trace":"g"}` {
		t.Fatalf("GET users: %d %s", code, body)
	}

	req := httptest.NewRequest("POST", "/echo", strings.NewReader(`{"name":"bob"}`))
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	vm.ServeHTTP(rec, req)
	if rec.Code != 201 || strings.TrimSpace(rec.Body.String()) != `{"got":"bob","user":"alice"}` || rec.Header().Get("X-Global") != "1" {
		t.Fatalf("POST echo: %d %s %v", rec.Code, rec.Body.String(), rec.Header())
	}

	if code, body = do(t, vm, "POST", "/echo", `{}`); code != 401 || !strings.Contains(body, `"no token"`) {
		t.Fatalf("unauthorized: %d %s", code, body)
	}

	req = httptest.NewRequest("POST", "/echo", strings.NewReader(`{bad json`))
	req.Header.Set("Authorization", "Bearer ok")
	rec = httptest.NewRecorder()
	vm.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("invalid json: %d %s", rec.Code, rec.Body.String())
	}

	code, body = do(t, vm, "GET", "/bad", "")
	var eb struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &eb); err != nil || code != 400 || eb.Message != "bad input" || eb.Data["field"] != "x" {
		t.Fatalf("ApiError: %d %s", code, body)
	}

	if code, body = do(t, vm, "GET", "/boom", ""); code != 500 || strings.Contains(body, "secret") {
		t.Fatalf("普通异常应 500 且不泄露细节: %d %s", code, body)
	}
	if code, body = do(t, vm, "DELETE", "/any", ""); code != 200 || body != "DELETE" {
		t.Fatalf("any method: %d %s", code, body)
	}
	if code, _ = do(t, vm, "POST", "/users/1", ""); code != http.StatusMethodNotAllowed {
		t.Fatalf("method mismatch: %d", code)
	}
	if code, _ = do(t, vm, "GET", "/twice", ""); code != 204 {
		t.Fatalf("twice: %d", code)
	}
}

// 处理函数可用闭包与顶层变量;顶层变量每个实例一份。并发请求下不应有数据竞争。
func TestRouter_ConcurrentClosures(t *testing.T) {
	vm := newVM(t, `
		let hits = 0;
		const greet = (name) => "hi " + name;
		routerAdd("GET", "/hello/{name}", (e) => { hits++; e.string(200, greet(e.request.pathValue("name"))); });
	`, jsvm.WithPoolSize(4))

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := strings.Repeat("x", i%5+1)
			if code, body := do(t, vm, "GET", "/hello/"+name, ""); code != 200 || body != "hi "+name {
				t.Errorf("got %d %q", code, body)
			}
		}()
	}
	wg.Wait()
}

func TestHooks(t *testing.T) {
	vm := newVM(t, `
		hookAdd("order.create", (e) => {
			if (e.amount <= 0) throw new BadRequestError("amount must be positive");
			e.amount = e.amount * 2;
			e.tags = ["vip"];
		});
		hookAdd("order.create", (e) => { e.second = true; });
		hookAdd("outer", (e) => { $app.trigger("order.create", e); e.outer = true; });
	`)
	ctx := context.Background()

	ev := map[string]any{"amount": 10}
	if err := vm.Trigger(ctx, "order.create", ev); err != nil {
		t.Fatal(err)
	}
	if ev["amount"] != int64(20) || ev["second"] != true {
		t.Fatalf("event 未被修改: %#v", ev)
	}

	err := vm.Trigger(ctx, "order.create", map[string]any{"amount": 0})
	var je *jsvm.Error
	if !errors.As(err, &je) || je.Status != 400 || je.Message != "amount must be positive" {
		t.Fatalf("应返回 400 *jsvm.Error: %v", err)
	}

	ev = map[string]any{"amount": 1}
	if err := vm.Trigger(ctx, "outer", ev); err != nil || ev["outer"] != true || ev["amount"] != int64(2) {
		t.Fatalf("$app.trigger 嵌套: %v %#v", err, ev)
	}

	if err := vm.Trigger(ctx, "none", nil); err != nil || vm.HasHook("none") || !vm.HasHook("outer") {
		t.Fatal("HasHook / 空钩子行为不对")
	}
}

func TestStartStopAndCron(t *testing.T) {
	events := make(chan string, 16)
	vm := newVM(t, `
		onStart(() => notify("start"));
		onStop(() => notify("stop"));
		cronAdd("tick", "@every 1s", () => notify("tick"));
		cronAdd("removed", "@every 1s", () => notify("removed"));
		cronRemove("removed");
	`, jsvm.WithGlobal("notify", func(s string) { events <- s }))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- vm.Start(ctx) }()

	want := []string{"start", "tick"}
	for _, w := range want {
		select {
		case got := <-events:
			if got != w {
				t.Fatalf("got %q, want %q", got, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("等待 %q 超时", w)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case got := <-events:
			if got == "removed" {
				t.Fatal("被 cronRemove 的任务不应执行")
			}
			if got == "stop" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("未触发 onStop")
		}
	}
}

func TestStart_HookErrorStopsApp(t *testing.T) {
	vm := newVM(t, `onStart(() => { throw new Error("cannot start"); });`)
	if err := vm.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Fatalf("onStart 失败应返回错误: %v", err)
	}
}

func TestEventBus(t *testing.T) {
	bus := eventbus.New[any]()
	got := make(chan any, 4)
	bus.Subscribe("js.out", func(_ string, p any) { got <- p })

	vm := newVM(t, `
		eventSubscribe("user.signup", (topic, p) => { $app.publish("js.out", { topic: topic, name: p.name }); });
		routerAdd("POST", "/pub", (e) => { e.json(200, { n: $app.publish("js.out", "from-route") }); });
	`, jsvm.WithEventBus(bus))

	bus.Publish("user.signup", map[string]any{"name": "carol"})
	select {
	case p := <-got:
		m, _ := p.(map[string]any)
		if m["topic"] != "user.signup" || m["name"] != "carol" {
			t.Fatalf("payload = %#v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("JS 订阅者未收到事件")
	}

	if code, body := do(t, vm, "POST", "/pub", ""); code != 200 || body != `{"n":1}` {
		t.Fatalf("publish: %d %s", code, body)
	}
	if p := <-got; p != "from-route" {
		t.Fatalf("payload = %#v", p)
	}
}

func TestSecurityBindings(t *testing.T) {
	vm := newVM(t, `
		routerAdd("GET", "/sec", (e) => {
			const key = "0123456789abcdef0123456789abcdef";
			const enc = $security.encrypt("hello", key);
			const hash = $security.hashPassword("pw");
			const tok = $security.createJWT({ sub: "u1" }, "s", 60);
			e.json(200, {
				rand: $security.randomString(16).length,
				alpha: $security.randomStringWithAlphabet(4, "a"),
				sha: $security.sha256("abc"),
				hs: $security.hs256("what do ya want for nothing?", "Jefe").slice(0, 8),
				eq: $security.equal("a", "a"),
				dec: $security.decrypt(enc, key),
				pw: $security.checkPassword(hash, "pw") && !$security.checkPassword(hash, "x"),
				sub: $security.parseJWT(tok, "s").sub,
				unverified: $security.parseUnverifiedJWT(tok).sub,
			});
		});
		routerAdd("GET", "/badjwt", (e) => { $security.parseJWT("x.y.z", "s"); });
	`)
	code, body := do(t, vm, "GET", "/sec", "")
	want := `{"rand":16,"alpha":"aaaa","sha":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad","hs":"5bdcc146","eq":true,"dec":"hello","pw":true,"sub":"u1","unverified":"u1"}`
	if code != 200 || body != want {
		t.Fatalf("security: %d %s", code, body)
	}
	if code, _ = do(t, vm, "GET", "/badjwt", ""); code != 500 {
		t.Fatalf("非法 JWT 应抛异常: %d", code)
	}
}

func TestHTTPBinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Up", "yes")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method": r.Method, "ct": r.Header.Get("Content-Type"), "auth": r.Header.Get("X-Token"), "body": string(b),
		})
	}))
	defer upstream.Close()

	vm := newVM(t, `
		routerAdd("GET", "/proxy", (e) => {
			const res = $http.send({ url: upstreamURL, method: "post", headers: { "X-Token": "t" }, body: { a: 1 }, timeout: 5 });
			e.json(200, { status: res.statusCode, up: res.headers["x-up"], echo: res.json });
		});
	`, jsvm.WithGlobal("upstreamURL", upstream.URL))

	code, body := do(t, vm, "GET", "/proxy", "")
	want := `{"status":200,"up":"yes","echo":{"auth":"t","body":"{\"a\":1}","ct":"application/json","method":"POST"}}`
	if code != 200 || body != want {
		t.Fatalf("http: %d %s", code, body)
	}
}

func TestFilesystemBinding(t *testing.T) {
	bucket := blob.NewMemory()
	vm := newVM(t, `
		routerAdd("PUT", "/files/{key...}", (e) => {
			$filesystem.put(e.request.pathValue("key"), e.request.text(), { contentType: "text/plain", metadata: { by: "js" } });
			e.noContent(204);
		});
		routerAdd("GET", "/files/{key...}", (e) => {
			const key = e.request.pathValue("key");
			const st = $filesystem.stat(key);
			if (!st) throw new NotFoundError();
			const buf = new Uint8Array($filesystem.get(key));
			e.json(200, { text: $filesystem.getString(key), len: buf.length, meta: st.metadata.by, list: $filesystem.list("docs/").map((a) => a.key) });
		});
	`, jsvm.WithBucket(bucket))

	if code, _ := do(t, vm, "PUT", "/files/docs/a.txt", "hello"); code != 204 {
		t.Fatalf("put: %d", code)
	}
	code, body := do(t, vm, "GET", "/files/docs/a.txt", "")
	if code != 200 || body != `{"text":"hello","len":5,"meta":"js","list":["docs/a.txt"]}` {
		t.Fatalf("get: %d %s", code, body)
	}
	if code, _ = do(t, vm, "GET", "/files/docs/missing", ""); code != 404 {
		t.Fatalf("missing: %d", code)
	}
	a, err := bucket.Stat(context.Background(), "docs/a.txt")
	if err != nil || a.Metadata["by"] != "js" {
		t.Fatalf("Go 侧应可见: %+v %v", a, err)
	}

	noBucket := newVM(t, `routerAdd("GET", "/x", (e) => { $filesystem.exists("a"); });`)
	if code, _ = do(t, noBucket, "GET", "/x", ""); code != 500 {
		t.Fatalf("未配置 bucket 应抛异常: %d", code)
	}
}

func TestOSBinding(t *testing.T) {
	off := newVM(t, `routerAdd("GET", "/os", (e) => e.string(200, typeof $os));`)
	if _, body := do(t, off, "GET", "/os", ""); body != "undefined" {
		t.Fatalf("$os 默认应关闭: %s", body)
	}
	on := newVM(t, `routerAdd("GET", "/os", (e) => {
		const r = $os.exec("echo", "hi");
		e.json(200, { out: r.stdout.trim(), code: r.exitCode });
	});`, jsvm.WithOS(true))
	if code, body := do(t, on, "GET", "/os", ""); code != 200 || body != `{"out":"hi","code":0}` {
		t.Fatalf("exec: %d %s", code, body)
	}
}

func TestTimeoutInterruptsAndInstanceIsReusable(t *testing.T) {
	vm := newVM(t, `
		routerAdd("GET", "/loop", () => { for (;;) {} });
		routerAdd("GET", "/sleep", () => { sleep(10000); });
		routerAdd("GET", "/ok", (e) => e.string(200, "ok"));
	`, jsvm.WithPoolSize(1), jsvm.WithTimeout(100*time.Millisecond))

	start := time.Now()
	if code, _ := do(t, vm, "GET", "/loop", ""); code != 500 {
		t.Fatalf("死循环应被中断: %d", code)
	}
	if code, _ := do(t, vm, "GET", "/sleep", ""); code != 500 {
		t.Fatalf("sleep 应被中断: %d", code)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("中断太慢")
	}
	if code, body := do(t, vm, "GET", "/ok", ""); code != 200 || body != "ok" {
		t.Fatalf("中断后实例应可复用: %d %s", code, body)
	}
}

func TestGoPanicRebuildsInstance(t *testing.T) {
	vm := newVM(t, `
		let calls = 0;
		routerAdd("GET", "/panic", () => { explode(); });
		routerAdd("GET", "/count", (e) => { calls++; e.string(200, String(calls)); });
	`, jsvm.WithPoolSize(1), jsvm.WithGlobal("explode", func() { panic("go bug") }))

	do(t, vm, "GET", "/count", "")
	if _, body := do(t, vm, "GET", "/count", ""); body != "2" {
		t.Fatalf("count = %s", body)
	}
	if code, _ := do(t, vm, "GET", "/panic", ""); code != 500 {
		t.Fatalf("panic: %d", code)
	}
	if code, body := do(t, vm, "GET", "/count", ""); code != 200 || body != "1" {
		t.Fatalf("panic 后应换成新实例(计数归零): %d %s", code, body)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"syntax":         `routerAdd("GET", "/x", (e) => {`,
		"throw":          `throw new Error("load fail")`,
		"route conflict": `routerAdd("GET", "/x", () => {}); routerAdd("GET", "/x", () => {});`,
		"bad pattern":    `routerAdd("GET", "no-slash", () => {});`,
		"bad cron":       `cronAdd("c", "not a spec", () => {});`,
		"not function":   `routerAdd("GET", "/x", 1);`,
		"no bus":         `eventSubscribe("t", () => {});`,
		"nondeterministic": `
			globalThis.__n = (globalThis.__n || 0);
			if (Math.random() < 0.5) routerAdd("GET", "/a", () => {});
			for (let i = 0; i < 20; i++) { if (Math.random() < 0.5) hookAdd("h" + i, () => {}); }`,
	}
	for name, script := range cases {
		if _, err := jsvm.New(jsvm.WithScript(name+".js", script), jsvm.WithPoolSize(4)); err == nil {
			t.Errorf("%s: 应加载失败", name)
		}
	}
}

func TestRegisterAfterLoadFails(t *testing.T) {
	vm := newVM(t, `routerAdd("GET", "/late", (e) => {
		try { routerAdd("GET", "/x", () => {}); } catch (err) { return e.string(200, "rejected"); }
		e.string(200, "accepted");
	});`)
	if _, body := do(t, vm, "GET", "/late", ""); body != "rejected" {
		t.Fatalf("加载后注册应被拒绝: %s", body)
	}
}

func TestStoreSharedAcrossInstances(t *testing.T) {
	vm := newVM(t, `
		routerAdd("POST", "/inc", (e) => {
			const n = ($app.store().get("n") || 0) + 1;
			$app.store().set("n", n);
			e.json(200, n);
		});
	`, jsvm.WithPoolSize(1))
	for i := 1; i <= 3; i++ {
		if _, body := do(t, vm, "POST", "/inc", ""); body != strconv.Itoa(i) {
			t.Fatalf("第 %d 次 = %s", i, body)
		}
	}
}

func TestWithFSAndDirOrdering(t *testing.T) {
	dir := t.TempDir()
	write := func(name, code string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.js", `routerAdd("GET", "/order", (e) => e.string(200, order.join(",")));`)
	write("a.js", `var order = ["a"];`)
	write("ignored.txt", `throw new Error("should not load")`)
	vm, err := jsvm.New(jsvm.WithDir(dir), jsvm.WithScript("c.js", `order.push("c");`))
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	if _, body := do(t, vm, "GET", "/order", ""); body != "a,c" {
		t.Fatalf("加载顺序 = %s", body)
	}
}
