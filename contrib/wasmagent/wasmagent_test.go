package wasmagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/wasm"
	"github.com/rushteam/beauty/contrib/wasmagent"
)

// ===== 极小 wasm 编码器(复用 contrib/wasm 测试的构造方式)=====

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
	body := append([]byte{0x00}, instrs...)
	return append(uleb(uint32(len(body))), body...)
}

func memMin(pages uint32) []byte { return append([]byte{0x00}, uleb(pages)...) }

func dataActive(off uint32, data []byte) []byte {
	out := []byte{0x00, 0x41}
	out = append(out, sleb(int64(off))...)
	out = append(out, 0x0b)
	return append(out, vecBytes(data)...)
}

var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// buildGuest 构造一个符合 wasmagent ABI 的 guest:
// alloc(i32)->i32 返回固定地址 1024;handle(i32,i32)->i64 忽略输入,返回 data 段的固定文本。
func buildGuest(response string) []byte {
	const dataOff = 16
	resp := []byte(response)
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
	packed := (int64(dataOff) << 32) | int64(len(resp))
	handleInstrs := append([]byte{0x42}, sleb(packed)...)
	handleInstrs = append(handleInstrs, 0x0b)
	m = append(m, section(10, vecItems(codeEntry(allocInstrs), codeEntry(handleInstrs)))...)
	m = append(m, section(11, vecItems(dataActive(dataOff, resp)))...)
	return m
}

// buildSpinGuest: alloc 正常,handle 死循环,用于测超时。
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
	spin := []byte{0x03, 0x40, 0x0c, 0x00, 0x0b, 0x00, 0x0b}
	m = append(m, section(10, vecItems(codeEntry(allocInstrs), codeEntry(spin)))...)
	return m
}

// ===== 测试 =====

func newRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	ctx := context.Background()
	rt, err := wasm.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close(ctx) })
	return rt
}

func TestExec_ReturnsGuestOutput(t *testing.T) {
	rt := newRuntime(t)
	ctx := context.Background()

	wasmBytes := buildGuest("hello from wasm")
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wasm")
	if err := os.WriteFile(path, wasmBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	exec := wasmagent.NewWasmExecutor(rt)
	out, err := exec.Exec(ctx, path, dir, []string{"arg1"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if out != "hello from wasm" {
		t.Fatalf("got %q, want %q", out, "hello from wasm")
	}
}

func TestExec_CachesModule(t *testing.T) {
	rt := newRuntime(t)
	ctx := context.Background()

	wasmBytes := buildGuest("cached")
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.wasm")
	if err := os.WriteFile(path, wasmBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	exec := wasmagent.NewWasmExecutor(rt)
	out1, _ := exec.Exec(ctx, path, dir, nil)
	out2, _ := exec.Exec(ctx, path, dir, nil)
	if out1 != "cached" || out2 != "cached" {
		t.Fatalf("缓存后应返回同样结果: %q / %q", out1, out2)
	}
}

func TestExec_RecompilesOnMtimeChange(t *testing.T) {
	rt := newRuntime(t)
	ctx := context.Background()

	dir := t.TempDir()
	path := filepath.Join(dir, "evolve.wasm")
	if err := os.WriteFile(path, buildGuest("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := wasmagent.NewWasmExecutor(rt)
	out, _ := exec.Exec(ctx, path, dir, nil)
	if out != "v1" {
		t.Fatalf("第一次 got %q", out)
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, buildGuest("v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _ = exec.Exec(ctx, path, dir, nil)
	if out != "v2" {
		t.Fatalf("mtime 变更后应重编译, got %q", out)
	}
}

func TestExec_FileNotFound(t *testing.T) {
	rt := newRuntime(t)
	exec := wasmagent.NewWasmExecutor(rt)
	_, err := exec.Exec(context.Background(), "/nonexist/x.wasm", "/tmp", nil)
	if err == nil {
		t.Fatal("应报错")
	}
}

func TestExec_Timeout(t *testing.T) {
	rt := newRuntime(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "spin.wasm")
	if err := os.WriteFile(path, buildSpinGuest(), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := wasmagent.NewWasmExecutor(rt, wasmagent.WithTimeout(50*time.Millisecond))
	start := time.Now()
	_, err := exec.Exec(context.Background(), path, dir, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("死循环应超时报错")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("应及时返回, 实际耗时 %s", elapsed)
	}
}

func TestExec_ConcurrentSafe(t *testing.T) {
	rt := newRuntime(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.wasm")
	if err := os.WriteFile(path, buildGuest("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := wasmagent.NewWasmExecutor(rt, wasmagent.WithPool(4))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				out, err := exec.Exec(context.Background(), path, dir, []string{"x"})
				if err != nil || out != "ok" {
					t.Errorf("并发错误: out=%q err=%v", out, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestToolFrom_ReturnsGuestOutput(t *testing.T) {
	rt := newRuntime(t)
	ctx := context.Background()

	mod, err := rt.Compile(ctx, buildGuest("tool-result"))
	if err != nil {
		t.Fatal(err)
	}

	tool := wasmagent.ToolFrom(mod, "test_tool", "a test tool",
		json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`))

	if tool.Def.Name != "test_tool" || tool.Def.Description != "a test tool" {
		t.Fatalf("Def 字段不对: %+v", tool.Def)
	}

	out, err := tool.Call(ctx, json.RawMessage(`{"x":"hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "tool-result" {
		t.Fatalf("got %q, want %q", out, "tool-result")
	}
}

func TestToolFrom_ConcurrentSafe(t *testing.T) {
	rt := newRuntime(t)
	ctx := context.Background()

	mod, err := rt.Compile(ctx, buildGuest("concurrent-tool"))
	if err != nil {
		t.Fatal(err)
	}

	tool := wasmagent.ToolFrom(mod, "conc", "concurrent tool",
		json.RawMessage(`{}`), wasmagent.WithPool(4))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				out, err := tool.Call(ctx, json.RawMessage(`{}`))
				if err != nil || out != "concurrent-tool" {
					t.Errorf("并发错误: out=%q err=%v", out, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestToolFrom_Timeout(t *testing.T) {
	rt := newRuntime(t)
	ctx := context.Background()

	mod, err := rt.Compile(ctx, buildSpinGuest())
	if err != nil {
		t.Fatal(err)
	}

	tool := wasmagent.ToolFrom(mod, "spin", "timeout test",
		json.RawMessage(`{}`), wasmagent.WithTimeout(50*time.Millisecond))

	start := time.Now()
	_, err = tool.Call(ctx, json.RawMessage(`{}`))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("死循环应超时报错")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("应及时返回, 耗时 %s", elapsed)
	}
}

func TestWithFuncNames(t *testing.T) {
	const dataOff = 16
	resp := []byte("custom-fn")
	m := append([]byte{}, wasmMagic...)
	tAlloc := funcType([]byte{i32}, []byte{i32})
	tHandle := funcType([]byte{i32, i32}, []byte{i64})
	m = append(m, section(1, vecItems(tAlloc, tHandle))...)
	m = append(m, section(3, vecItems(uleb(0), uleb(1)))...)
	m = append(m, section(5, vecItems(memMin(1)))...)
	m = append(m, section(7, vecItems(
		exportEntry("memory", 0x02, 0),
		exportEntry("my_alloc", 0x00, 0),
		exportEntry("my_handle", 0x00, 1),
	))...)
	allocInstrs := append([]byte{0x41}, sleb(1024)...)
	allocInstrs = append(allocInstrs, 0x0b)
	packed := (int64(dataOff) << 32) | int64(len(resp))
	handleInstrs := append([]byte{0x42}, sleb(packed)...)
	handleInstrs = append(handleInstrs, 0x0b)
	m = append(m, section(10, vecItems(codeEntry(allocInstrs), codeEntry(handleInstrs)))...)
	m = append(m, section(11, vecItems(dataActive(dataOff, resp)))...)

	rt := newRuntime(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.wasm")
	if err := os.WriteFile(path, m, 0o644); err != nil {
		t.Fatal(err)
	}

	exec := wasmagent.NewWasmExecutor(rt, wasmagent.WithFuncNames("my_alloc", "my_handle"))
	out, err := exec.Exec(context.Background(), path, dir, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if out != "custom-fn" {
		t.Fatalf("got %q, want %q", out, "custom-fn")
	}
}
