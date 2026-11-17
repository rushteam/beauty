package jsvm_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/jsvm"
)

func writeFile(t *testing.T, name, code string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRequire(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.js"), `
		const math = require("./lib/math");
		const conf = require("./lib/conf.json");
		const viaIndex = require("./lib/pkg");
		let local = "entry";
		routerAdd("GET", "/sum", (e) => {
			e.string(200, [
				math.add(2, 3),
				require("./lib/math").add(10, 1),
				conf.rate,
				viaIndex.name,
				typeof secret,
				local,
			].join(","));
		});
	`)
	writeFile(t, filepath.Join(dir, "lib", "math.js"), `
		var secret = "hidden";
		let loads = 0;
		loads++;
		function add(a, b) { return a + b; }
		module.exports = { add: add, loads: loads };
	`)
	writeFile(t, filepath.Join(dir, "lib", "conf.json"), `{"rate": 0.9}`)
	writeFile(t, filepath.Join(dir, "lib", "pkg", "index.js"), `module.exports = { name: "pkg" };`)
	writeFile(t, filepath.Join(dir, "lib", "a.js"), `
		exports.a = 1;
		const b = require("./b.js");
		exports.fromB = b.fromA;
		exports.b = b.b;
	`)
	writeFile(t, filepath.Join(dir, "lib", "b.js"), `
		const a = require("./a.js");
		exports.fromA = a.a;
		exports.b = 2;
	`)
	writeFile(t, filepath.Join(dir, "lib", "escape.js"), `module.exports = function () { require("../secret"); };`)

	vm, err := jsvm.New(jsvm.WithDir(dir), jsvm.WithPoolSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()

	if _, body := do(t, vm, "GET", "/sum", ""); body != "5,11,0.9,pkg,undefined,entry" {
		t.Fatalf("require = %s", body)
	}
	// 同一实例内模块只执行一次:第二次请求 loads 仍来自缓存。
	if _, body := do(t, vm, "GET", "/sum", ""); body != "5,11,0.9,pkg,undefined,entry" {
		t.Fatalf("cached = %s", body)
	}

	// 内联脚本没有文件位置,相对路径无法解析;裸路径从 WithModuleFS 查找。
	if _, err := jsvm.New(jsvm.WithScript("t.js", `require("./nope.js")`)); err == nil {
		t.Fatal("内联脚本的相对 require 应失败")
	}
	bare := newVM(t, `
		routerAdd("GET", "/cycle", (e) => {
			const a = require("a");
			e.string(200, [a.a, a.fromB, a.b].join(","));
		});
		routerAdd("GET", "/escape", () => { require("escape")(); });
	`, jsvm.WithModuleFS(os.DirFS(filepath.Join(dir, "lib"))))
	if _, body := do(t, bare, "GET", "/cycle", ""); body != "1,1,2" {
		t.Fatalf("cycle = %s", body)
	}
	if code, _ := do(t, bare, "GET", "/escape", ""); code != 500 {
		t.Fatalf("逃逸模块根应失败: %d", code)
	}
}

func TestReload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.js"), `
		const msg = require("./lib/msg").text;
		routerAdd("GET", "/m", (e) => e.string(200, msg));
		routerAdd("GET", "/only-old", (e) => e.string(200, "old"));
		hookAdd("h", (e) => { e.v = 1; });
		onReload(() => { $app.store().set("reloaded", ($app.store().get("reloaded") || 0) + 1); });
	`)
	writeFile(t, filepath.Join(dir, "lib", "msg.js"), `module.exports = { text: "v1" };`)

	vm, err := jsvm.New(jsvm.WithDir(dir), jsvm.WithPoolSize(2))
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	if _, body := do(t, vm, "GET", "/m", ""); body != "v1" {
		t.Fatalf("before = %s", body)
	}

	writeFile(t, filepath.Join(dir, "lib", "msg.js"), `module.exports = { text: "v2" };`)
	writeFile(t, filepath.Join(dir, "main.js"), `
		const msg = require("./lib/msg").text;
		routerAdd("GET", "/m", (e) => e.string(200, msg));
		routerAdd("GET", "/reloads", (e) => e.string(200, String($app.store().get("reloaded"))));
		hookAdd("h", (e) => { e.v = 2; });
		onReload(() => { $app.store().set("reloaded", ($app.store().get("reloaded") || 0) + 1); });
	`)
	if err := vm.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, body := do(t, vm, "GET", "/m", ""); body != "v2" {
		t.Fatalf("after = %s", body)
	}
	if code, _ := do(t, vm, "GET", "/only-old", ""); code != 404 {
		t.Fatalf("删除的路由应消失: %d", code)
	}
	ev := map[string]any{}
	if err := vm.Trigger(context.Background(), "h", ev); err != nil || ev["v"] != int64(2) {
		t.Fatalf("hook = %v, %v", ev, err)
	}
	if _, body := do(t, vm, "GET", "/reloads", ""); body != "1" {
		t.Fatalf("onReload 次数 = %s", body)
	}

	// 语法错误的重载保持旧一代。
	writeFile(t, filepath.Join(dir, "main.js"), `routerAdd("GET", "/m", (e) => {`)
	if err := vm.Reload(context.Background()); err == nil {
		t.Fatal("语法错误应使 Reload 失败")
	}
	if _, body := do(t, vm, "GET", "/m", ""); body != "v2" {
		t.Fatalf("失败的重载不应影响服务: %s", body)
	}
}

func TestReload_KeepsInflightOnOldGeneration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.js"), `
		routerAdd("GET", "/t", (e) => { entered(); release(); e.string(200, "v1"); });
	`)
	entered := make(chan struct{})
	var once sync.Once
	release := make(chan struct{})
	vm, err := jsvm.New(jsvm.WithDir(dir), jsvm.WithPoolSize(1),
		jsvm.WithGlobal("entered", func() { once.Do(func() { close(entered) }) }),
		jsvm.WithGlobal("release", func() { <-release }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()

	done := make(chan string, 1)
	go func() {
		_, body := do(t, vm, "GET", "/t", "")
		done <- body
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("请求没有进入处理函数")
	}

	writeFile(t, filepath.Join(dir, "main.js"), `
		routerAdd("GET", "/t", (e) => e.string(200, "v2"));
	`)
	if err := vm.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)

	select {
	case body := <-done:
		if body != "v1" {
			t.Fatalf("在途请求应跑完旧脚本, got %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("在途请求没有结束")
	}
	if _, body := do(t, vm, "GET", "/t", ""); body != "v2" {
		t.Fatalf("新请求应使用新脚本, got %s", body)
	}
}

func TestWatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.js"), `routerAdd("GET", "/m", (e) => e.string(200, "v1"));`)
	vm, err := jsvm.New(jsvm.WithDir(dir), jsvm.WithPoolSize(1), jsvm.WithWatch(true), jsvm.WithWatchDelay(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = vm.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, body := do(t, vm, "GET", "/m", ""); body == "v1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("初始脚本没有生效")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 第一次写入可能发生在监听器就绪之前,所以持续写到切换成功为止。
	deadline = time.Now().Add(3 * time.Second)
	for {
		writeFile(t, filepath.Join(dir, "main.js"), `routerAdd("GET", "/m", (e) => e.string(200, "v2"));`)
		time.Sleep(100 * time.Millisecond)
		_, body := do(t, vm, "GET", "/m", "")
		if body == "v2" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("监听没有触发重载,仍是 %s", body)
		}
	}
}

func TestReload_StoreSurvives(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.js"), `
		routerAdd("POST", "/n", (e) => { $app.store().set("n", 7); e.noContent(204); });
		routerAdd("GET", "/n", (e) => e.string(200, String($app.store().get("n"))));
	`)
	vm, err := jsvm.New(jsvm.WithDir(dir), jsvm.WithPoolSize(1))
	if err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	do(t, vm, "POST", "/n", "")
	writeFile(t, filepath.Join(dir, "main.js"), `
		routerAdd("GET", "/n", (e) => e.string(200, String($app.store().get("n"))));
	`)
	if err := vm.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, body := do(t, vm, "GET", "/n", ""); body != "7" {
		t.Fatalf("store = %s", body)
	}
}

func TestRequire_NotFoundIsError(t *testing.T) {
	vm := newVM(t, `routerAdd("GET", "/m", () => { require("missing"); });`, jsvm.WithModuleFS(os.DirFS(t.TempDir())))
	if code, _ := do(t, vm, "GET", "/m", ""); code != 500 {
		t.Fatalf("code = %d", code)
	}
}
