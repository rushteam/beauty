package wasm_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"

	"github.com/rushteam/beauty/contrib/wasm"
)

// ===== 极小 wasm 编码器(仅供测试构造 guest,无需工具链/WASI)=====

const (
	i32 = 0x7f
	i64 = 0x7e
)

func uleb(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
		} else {
			return append(out, b)
		}
	}
}

func sleb(v int64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

func vecBytes(b []byte) []byte { return append(uleb(uint32(len(b))), b...) }

func vecItems(items ...[]byte) []byte {
	out := uleb(uint32(len(items)))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func section(id byte, payload []byte) []byte {
	return append([]byte{id}, append(uleb(uint32(len(payload))), payload...)...)
}

func funcType(params, results []byte) []byte {
	out := []byte{0x60}
	out = append(out, vecBytes(params)...)
	return append(out, vecBytes(results)...)
}

func exportEntry(name string, kind byte, idx uint32) []byte {
	out := vecBytes([]byte(name))
	out = append(out, kind)
	return append(out, uleb(idx)...)
}

func codeEntry(instrs []byte) []byte {
	body := append([]byte{0x00}, instrs...) // 0 local decls
	return append(uleb(uint32(len(body))), body...)
}

func memMin(pages uint32) []byte { return append([]byte{0x00}, uleb(pages)...) }

func dataActive(off uint32, data []byte) []byte {
	out := []byte{0x00, 0x41} // memidx 0, i32.const
	out = append(out, sleb(int64(off))...)
	out = append(out, 0x0b) // end
	return append(out, vecBytes(data)...)
}

var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// add(i32,i32)->i32
func buildAdd() []byte {
	m := append([]byte{}, wasmMagic...)
	m = append(m, section(1, vecItems(funcType([]byte{i32, i32}, []byte{i32})))...)
	m = append(m, section(3, vecItems(uleb(0)))...)
	m = append(m, section(7, vecItems(exportEntry("add", 0x00, 0)))...)
	instrs := []byte{0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b} // local.get0 local.get1 i32.add end
	m = append(m, section(10, vecItems(codeEntry(instrs)))...)
	return m
}

// 导入 env.log(i32,i32),导出 memory 与 run();run 调用 log(0,2),内存 [0,2)="hi"。
func buildLog(memPages uint32) []byte {
	m := append([]byte{}, wasmMagic...)
	t0 := funcType([]byte{i32, i32}, nil) // log
	t1 := funcType(nil, nil)              // run
	m = append(m, section(1, vecItems(t0, t1))...)
	m = append(m, section(2, vecItems(importFunc("env", "log", 0)))...)
	m = append(m, section(3, vecItems(uleb(1)))...) // run: type1
	m = append(m, section(5, vecItems(memMin(memPages)))...)
	m = append(m, section(7, vecItems(exportEntry("memory", 0x02, 0), exportEntry("run", 0x00, 1)))...)
	// code 在 data 之前(section id 升序:10 < 11)
	instrs := []byte{0x41, 0x00, 0x41, 0x02, 0x10, 0x00, 0x0b} // i32.const0 i32.const2 call0 end
	m = append(m, section(10, vecItems(codeEntry(instrs)))...)
	m = append(m, section(11, vecItems(dataActive(0, []byte("hi"))))...)
	return m
}

func importFunc(mod, name string, typeidx uint32) []byte {
	out := vecBytes([]byte(mod))
	out = append(out, vecBytes([]byte(name))...)
	out = append(out, 0x00) // func import
	return append(out, uleb(typeidx)...)
}

// 中间件 guest:alloc(i32)->i32 返回常量 1024;handle(i32,i32)->i64 返回打包 (16<<32|len),
// 决策 JSON 放在内存偏移 16 的 data 段。忽略输入,返回固定决策。
func buildMiddleware(decision []byte) []byte {
	const dataOff = 16
	m := append([]byte{}, wasmMagic...)
	tAlloc := funcType([]byte{i32}, []byte{i32})
	tHandle := funcType([]byte{i32, i32}, []byte{i64})
	m = append(m, section(1, vecItems(tAlloc, tHandle))...)
	m = append(m, section(3, vecItems(uleb(0), uleb(1)))...) // alloc:t0, handle:t1
	m = append(m, section(5, vecItems(memMin(1)))...)
	m = append(m, section(7, vecItems(
		exportEntry("memory", 0x02, 0),
		exportEntry("alloc", 0x00, 0),
		exportEntry("handle", 0x00, 1),
	))...)
	allocInstrs := append([]byte{0x41}, sleb(1024)...) // i32.const 1024
	allocInstrs = append(allocInstrs, 0x0b)            // end
	packed := (int64(dataOff) << 32) | int64(len(decision))
	handleInstrs := append([]byte{0x42}, sleb(packed)...) // i64.const packed
	handleInstrs = append(handleInstrs, 0x0b)             // end
	m = append(m, section(10, vecItems(codeEntry(allocInstrs), codeEntry(handleInstrs)))...)
	m = append(m, section(11, vecItems(dataActive(dataOff, decision)))...)
	return m
}

// ===== 测试 =====

func TestRuntime_CallAdd(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildAdd())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	inst, err := mod.Instantiate(ctx)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(ctx)

	res, err := inst.Call(ctx, "add", api.EncodeI32(2), api.EncodeI32(3))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := api.DecodeI32(res[0]); got != 5 {
		t.Fatalf("add(2,3) = %d, want 5", got)
	}
}

func TestRuntime_HostFunc(t *testing.T) {
	ctx := context.Background()
	var captured string
	rt, err := wasm.New(ctx, wasm.WithHostFunc("env", "log",
		func(_ context.Context, m api.Module, ptr, n uint32) {
			b, _ := m.Memory().Read(ptr, n)
			captured = string(b)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	mod, err := rt.Compile(ctx, buildLog(1))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	inst, err := mod.Instantiate(ctx)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(ctx)

	if _, err := inst.Call(ctx, "run"); err != nil {
		t.Fatalf("call run: %v", err)
	}
	if captured != "hi" {
		t.Fatalf("host func 收到 %q, want hi", captured)
	}
}

func TestRuntime_MemoryLimit(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx, wasm.WithMemoryLimitPages(1))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	// guest 声明 min 3 页,超过 1 页上限,应被拒(wazero 在编译期即校验内存上限)。
	mod, err := rt.Compile(ctx, buildLog(3))
	if err != nil {
		return // 编译期即拒绝,符合预期
	}
	if _, err := mod.Instantiate(ctx); err == nil {
		t.Fatal("超内存上限应被拒(编译或实例化任一处)")
	}
}

func newMW(t *testing.T, decision string, opts ...wasm.MiddlewareOption) http.Handler {
	t.Helper()
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	mod, err := rt.Compile(ctx, buildMiddleware([]byte(decision)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("downstream"))
	})
	return wasm.Middleware(mod, opts...)(next)
}

func TestMiddleware_Allow(t *testing.T) {
	h := newMW(t, `{"action":"next"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "downstream" {
		t.Fatalf("放行应到下游: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestMiddleware_Deny(t *testing.T) {
	h := newMW(t, `{"action":"deny","status":403,"body":"blocked"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("应拦截 403, got %d", rec.Code)
	}
	if rec.Body.String() != "blocked" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestMiddleware_DenyDefaultStatus(t *testing.T) {
	h := newMW(t, `{"action":"deny"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("deny 默认应 403, got %d", rec.Code)
	}
}

// guest 缺 handle 导出 → 出错;默认 fail-closed 返回 500,WithFailOpen 则放行。
func TestMiddleware_FailClosedAndOpen(t *testing.T) {
	ctx := context.Background()
	rt, _ := wasm.New(ctx)
	t.Cleanup(func() { rt.Close(ctx) })
	broken, err := rt.Compile(ctx, buildAdd()) // 没有 alloc/handle
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// fail-closed(默认)
	rec := httptest.NewRecorder()
	wasm.Middleware(broken)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("fail-closed 应 500, got %d", rec.Code)
	}

	// fail-open
	rec2 := httptest.NewRecorder()
	wasm.Middleware(broken, wasm.WithFailOpen(true))(next).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("fail-open 应放行 200, got %d", rec2.Code)
	}
}

// buildSpinGuest:alloc 正常返回,handle 是死循环(loop{br 0} unreachable),用于测超时中断。
func buildSpinGuest() []byte {
	m := append([]byte{}, wasmMagic...)
	tAlloc := funcType([]byte{i32}, []byte{i32})
	tHandle := funcType([]byte{i32, i32}, []byte{i64})
	m = append(m, section(1, vecItems(tAlloc, tHandle))...)
	m = append(m, section(3, vecItems(uleb(0), uleb(1)))...)
	m = append(m, section(5, vecItems(memMin(1)))...)
	m = append(m, section(7, vecItems(
		exportEntry("memory", 0x02, 0),
		exportEntry("alloc", 0x00, 0),
		exportEntry("handle", 0x00, 1),
	))...)
	allocInstrs := append([]byte{0x41}, sleb(1024)...)
	allocInstrs = append(allocInstrs, 0x0b)
	spin := []byte{0x03, 0x40, 0x0c, 0x00, 0x0b, 0x00, 0x0b} // loop(void){ br 0 } unreachable end
	m = append(m, section(10, vecItems(codeEntry(allocInstrs), codeEntry(spin)))...)
	return m
}

// 死循环 guest + WithTimeout:执行被中断,fail-closed 返回 500,且在超时附近返回(不挂死)。
func TestMiddleware_Timeout(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	mod, err := rt.Compile(ctx, buildSpinGuest())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := wasm.Middleware(mod, wasm.WithTimeout(50*time.Millisecond))(next)

	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("超时应 fail-closed 500, got %d", rec.Code)
	}
	if elapsed > time.Second {
		t.Fatalf("应在超时附近返回(未中断?), 用时 %s", elapsed)
	}
}

// 改写:guest 放行的同时注入/删除请求头,下游应看到改写后的请求。
func TestMiddleware_ModifyRequestHeaders(t *testing.T) {
	dec := `{"action":"next","setRequestHeaders":{"X-User":"alice"},"removeRequestHeaders":["X-Secret"]}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	mod, err := rt.Compile(ctx, buildMiddleware([]byte(dec)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var gotUser, gotSecret string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = r.Header.Get("X-User")
		gotSecret = r.Header.Get("X-Secret")
		w.WriteHeader(http.StatusOK)
	})
	h := wasm.Middleware(mod)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Secret", "leak")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("放行应到下游, code=%d", rec.Code)
	}
	if gotUser != "alice" {
		t.Fatalf("应注入 X-User=alice, got %q", gotUser)
	}
	if gotSecret != "" {
		t.Fatalf("应删除 X-Secret, got %q", gotSecret)
	}
}

// 追加:AddRequestHeaders 保留已有值并追加(多值头),Set 则覆盖。
func TestMiddleware_AddRequestHeaders(t *testing.T) {
	dec := `{"action":"next","addRequestHeaders":{"X-Trace":["a","b"]}}`
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	mod, err := rt.Compile(ctx, buildMiddleware([]byte(dec)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var got []string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("X-Trace")
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("X-Trace", "orig") // 已有值应保留
	rec := httptest.NewRecorder()
	wasm.Middleware(mod)(next).ServeHTTP(rec, req)

	if len(got) != 3 || got[0] != "orig" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("Add 应保留已有并追加, got %v", got)
	}
}

// buildClockGuest:导入 env.now_unix_milli()->i64,导出 run()->i64 直接转调它。
func buildClockGuest() []byte {
	m := append([]byte{}, wasmMagic...)
	t := funcType(nil, []byte{i64}) // ()->i64,now 与 run 同型
	m = append(m, section(1, vecItems(t))...)
	m = append(m, section(2, vecItems(importFunc("env", "now_unix_milli", 0)))...)
	m = append(m, section(3, vecItems(uleb(0)))...) // run: type0
	m = append(m, section(7, vecItems(exportEntry("run", 0x00, 1)))...)
	instrs := []byte{0x10, 0x00, 0x0b} // call 0; end
	m = append(m, section(10, vecItems(codeEntry(instrs)))...)
	return m
}

// 内置 WithLog:guest 调 env.log(0,2) 打出内存里的 "hi"。
func TestHostFunc_WithLog(t *testing.T) {
	ctx := context.Background()
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := wasm.New(ctx, wasm.WithLog(logger))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	mod, err := rt.Compile(ctx, buildLog(1))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	inst, err := mod.Instantiate(ctx)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(ctx)
	if _, err := inst.Call(ctx, "run"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(buf.String(), "hi") {
		t.Fatalf("WithLog 应记录 guest 的日志, got %q", buf.String())
	}
}

// 内置 WithClock:guest 调 env.now_unix_milli() 拿到 >0 的毫秒时间戳。
func TestHostFunc_WithClock(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx, wasm.WithClock())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	mod, err := rt.Compile(ctx, buildClockGuest())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	inst, err := mod.Instantiate(ctx)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(ctx)
	res, err := inst.Call(ctx, "run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if int64(res[0]) <= 0 {
		t.Fatalf("now_unix_milli 应返回 >0 的时间戳, got %d", int64(res[0]))
	}
}

// 实例池 + 并发:池化复用下,大量并发请求仍应得到正确决策(-race 验证池并发安全)。
func TestMiddleware_PoolConcurrent(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	mod, err := rt.Compile(ctx, buildMiddleware([]byte(`{"action":"next"}`)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := wasm.Middleware(mod, wasm.WithPool(4))(next)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
				if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
					t.Errorf("池化并发下决策错误: code=%d body=%q", rec.Code, rec.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()
}

// 可观测:WithObserver 应在每次执行后收到动作/耗时。
func TestMiddleware_Observer(t *testing.T) {
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	mod, err := rt.Compile(ctx, buildMiddleware([]byte(`{"action":"deny","status":403}`)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var ev wasm.Event
	var n int
	h := wasm.Middleware(mod, wasm.WithObserver(func(e wasm.Event) { ev = e; n++ }))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if n != 1 {
		t.Fatalf("observer 应被调用一次, got %d", n)
	}
	if ev.Action != "deny" || ev.Err != nil {
		t.Fatalf("event = %+v", ev)
	}
}
