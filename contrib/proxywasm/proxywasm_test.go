package proxywasm

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 测试用手工构建最小 wasm 模块(与 contrib/wasm 同策略:无需外部工具链)。

// TestEncodeDecodeHeaderMap 测试头部二进制编解码。
func TestEncodeDecodeHeaderMap(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Custom", "value")

	data := encodeHeaderMap(h)
	pairs := decodeHeaderMap(data)

	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}

	found := map[string]string{}
	for _, p := range pairs {
		found[p[0]] = p[1]
	}
	if found["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q", found["Content-Type"])
	}
	if found["X-Custom"] != "value" {
		t.Errorf("X-Custom = %q", found["X-Custom"])
	}
}

// TestEncodeDecodeEmpty 测试空 header map。
func TestEncodeDecodeEmpty(t *testing.T) {
	h := http.Header{}
	data := encodeHeaderMap(h)
	pairs := decodeHeaderMap(data)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs, got %d", len(pairs))
	}
}

// TestDecodeNilData 测试 nil/短数据。
func TestDecodeNilData(t *testing.T) {
	if pairs := decodeHeaderMap(nil); pairs != nil {
		t.Fatal("expected nil")
	}
	if pairs := decodeHeaderMap([]byte{1}); pairs != nil {
		t.Fatal("expected nil for short data")
	}
}

// TestPropertySplit 测试 property path 解析。
func TestPropertySplit(t *testing.T) {
	segs := splitPropertyPath([]byte("request\x00path"))
	if len(segs) != 2 || segs[0] != "request" || segs[1] != "path" {
		t.Errorf("unexpected segments: %v", segs)
	}
}

// TestPluginStateGetProperty 测试 property 读取。
func TestPluginStateGetProperty(t *testing.T) {
	ps := newPluginState(nil, nil, LogInfo, map[string][]byte{
		"custom\x00key": []byte("custom-value"),
	}, nil, nil, nil, nil, nil)
	id := ps.allocContextID()
	ps.contexts[id] = &contextState{
		requestPath:   "/test",
		requestMethod: "POST",
	}
	ps.activeContextID = id

	// 内置 property
	val, res := ps.getProperty([]byte("request\x00path"))
	if res != WasmResultOk || string(val) != "/test" {
		t.Errorf("request.path = %q (result=%d)", val, res)
	}

	val, res = ps.getProperty([]byte("request\x00method"))
	if res != WasmResultOk || string(val) != "POST" {
		t.Errorf("request.method = %q (result=%d)", val, res)
	}

	// 自定义 property
	val, res = ps.getProperty([]byte("custom\x00key"))
	if res != WasmResultOk || string(val) != "custom-value" {
		t.Errorf("custom key = %q (result=%d)", val, res)
	}

	// 不存在的 property
	_, res = ps.getProperty([]byte("nonexistent"))
	if res != WasmResultNotFound {
		t.Errorf("expected NotFound, got %d", res)
	}
}

// TestBufferOps 测试 buffer 读写。
func TestBufferOps(t *testing.T) {
	ps := newPluginState([]byte("vm-cfg"), []byte("plugin-cfg"), LogInfo, nil, nil, nil, nil, nil, nil)
	id := ps.allocContextID()
	ps.contexts[id] = &contextState{
		requestBody:  []byte("hello body"),
		responseBody: []byte("resp data"),
	}
	ps.activeContextID = id

	// VM config
	if buf := ps.getBuffer(BufferTypeVMConfiguration); string(buf) != "vm-cfg" {
		t.Errorf("vm config = %q", buf)
	}
	// Plugin config
	if buf := ps.getBuffer(BufferTypePluginConfiguration); string(buf) != "plugin-cfg" {
		t.Errorf("plugin config = %q", buf)
	}
	// Request body
	if buf := ps.getBuffer(BufferTypeHTTPRequestBody); string(buf) != "hello body" {
		t.Errorf("request body = %q", buf)
	}
	// Set buffer
	ps.setBuffer(BufferTypeHTTPRequestBody, []byte("modified"))
	if buf := ps.getBuffer(BufferTypeHTTPRequestBody); string(buf) != "modified" {
		t.Errorf("modified body = %q", buf)
	}
}

// ==================== 集成测试 ====================

// buildMinimalGuest 构建一个最小的 Proxy-Wasm guest 模块:
//   - 导出 memory, proxy_abi_version_0_2_1, proxy_on_memory_allocate, proxy_on_vm_start,
//     proxy_on_configure, proxy_on_context_create, proxy_on_request_headers
//   - proxy_on_request_headers 固定返回 ActionContinue (0)
func buildMinimalGuest() []byte {
	return buildWasmModule(actionContinueGuest)
}

// buildDenyGuest 构建一个调用 send_local_response 拒绝请求的 guest。
func buildDenyGuest() []byte {
	return buildWasmModule(actionDenyGuest)
}

// actionContinueGuest: proxy_on_request_headers → return 0 (Continue)
var actionContinueGuest = guestSpec{
	onRequestHeadersAction: 0, // Continue
}

// actionDenyGuest: proxy_on_request_headers → call send_local_response(403,...) then return 0
var actionDenyGuest = guestSpec{
	onRequestHeadersAction: 0,
	sendLocalResponse:      true,
	localResponseStatus:    403,
	localResponseBody:      "blocked by wasm",
}

type guestSpec struct {
	onRequestHeadersAction uint32
	sendLocalResponse      bool
	localResponseStatus    uint32
	localResponseBody      string
}

// buildWasmModule 手工构建最小 wasm 二进制。
// 为保持测试可维护性,用 wazero 的测试辅助构建方式不可行(它不导出构建器),
// 所以我们直接编码 wasm binary format。
//
// 极简模块: 1 page memory, 几个导出函数,固定行为。
func buildWasmModule(spec guestSpec) []byte {
	// 由于手工编码完整的 wasm 二进制模块极其冗长且容易出错,
	// 这里使用 WAT (WebAssembly Text format) 的等价二进制编码。
	// 最小可行模块:
	// - 导入: env.proxy_send_local_response (如果需要 deny)
	// - 导出: memory, proxy_abi_version_0_2_1, proxy_on_memory_allocate,
	//         proxy_on_vm_start, proxy_on_configure, proxy_on_context_create,
	//         proxy_on_request_headers
	//
	// 内存布局: bump allocator from offset 1024

	var m wasmBuilder
	m.init()

	// Types section
	// type 0: () -> i32 (for vm_start, configure)
	// type 1: (i32) -> i32 (for memory_allocate)
	// type 2: (i32, i32) -> () (for context_create)
	// type 3: (i32, i32, i32) -> i32 (for request_headers)
	// type 4: (i32, i32, i32, i32, i32, i32, i32, i32) -> i32 (send_local_response)
	typeSection := []byte{
		5, // 5 types
		// type 0: (i32, i32) -> i32
		0x60, 2, 0x7f, 0x7f, 1, 0x7f,
		// type 1: (i32) -> i32
		0x60, 1, 0x7f, 1, 0x7f,
		// type 2: (i32, i32) -> ()
		0x60, 2, 0x7f, 0x7f, 0,
		// type 3: (i32, i32, i32) -> i32
		0x60, 3, 0x7f, 0x7f, 0x7f, 1, 0x7f,
		// type 4: (i32, i32, i32, i32, i32, i32, i32, i32) -> i32 (send_local_response)
		0x60, 8, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 0x7f, 1, 0x7f,
	}
	m.section(1, typeSection)

	// Import section (only if deny)
	var importedFuncs int
	if spec.sendLocalResponse {
		importSection := encodeImportSection("env", "proxy_send_local_response", 4)
		m.section(2, importSection)
		importedFuncs = 1
	}

	// Function section - declare functions
	funcSection := []byte{5, // 5 functions
		1, // proxy_on_memory_allocate → type1
		0, // proxy_on_vm_start → type0
		0, // proxy_on_configure → type0
		2, // proxy_on_context_create → type2
		3, // proxy_on_request_headers → type3
	}
	m.section(3, funcSection)

	// Memory section: 1 page min/max
	m.section(5, []byte{1, 0x01, 1, 1}) // 1 memory, limits: min=1, max=1

	// Global section: bump allocator pointer at offset 1024
	globalSection := []byte{
		1,          // 1 global
		0x7f, 0x01, // i32, mutable
		0x41, 0x80, 0x08, 0x0b, // i32.const 1024, end
	}
	m.section(6, globalSection)

	// Export section
	exports := []exportEntry{
		{name: "memory", kind: 0x02, index: 0},
		{name: "proxy_on_memory_allocate", kind: 0x00, index: uint32(importedFuncs) + 0},
		{name: "proxy_on_vm_start", kind: 0x00, index: uint32(importedFuncs) + 1},
		{name: "proxy_on_configure", kind: 0x00, index: uint32(importedFuncs) + 2},
		{name: "proxy_on_context_create", kind: 0x00, index: uint32(importedFuncs) + 3},
		{name: "proxy_on_request_headers", kind: 0x00, index: uint32(importedFuncs) + 4},
	}
	m.section(7, encodeExports(exports))

	// Code section
	var codes [][]byte

	// proxy_on_memory_allocate(size i32) -> i32: bump alloc
	codes = append(codes, encodeFunc([]byte{
		// get global 0 (current ptr)
		0x23, 0x00,
		// local.get 0 (size)
		0x20, 0x00,
		// i32.add → new ptr
		0x6a,
		// global.set 0
		0x24, 0x00,
		// return old ptr: global.get 0 - local.get 0
		0x23, 0x00,
		0x20, 0x00,
		0x6b, // i32.sub
		0x0b, // end
	}))

	// proxy_on_vm_start(ctx_id, config_size) -> i32: return 1
	codes = append(codes, encodeFunc([]byte{
		0x41, 0x01, // i32.const 1
		0x0b,
	}))

	// proxy_on_configure(ctx_id, config_size) -> i32: return 1
	codes = append(codes, encodeFunc([]byte{
		0x41, 0x01,
		0x0b,
	}))

	// proxy_on_context_create(ctx_id, parent_id): noop
	codes = append(codes, encodeFunc([]byte{0x0b}))

	// proxy_on_request_headers(ctx_id, num_headers, end_of_stream) -> i32
	if spec.sendLocalResponse {
		// 调用 send_local_response(403, 0, 0, body_ptr, body_len, 0, 0, -1)
		body := []byte(spec.localResponseBody)
		bodyOffset := 2048 // 把 body 放到 data section 的 offset 2048
		code := []byte{}
		// call imported func (index 0): send_local_response
		code = append(code, 0x41) // i32.const status
		code = append(code, encodeLEB128i(int32(spec.localResponseStatus))...)
		code = append(code, 0x41, 0x00) // status_detail ptr = 0
		code = append(code, 0x41, 0x00) // status_detail size = 0
		code = append(code, 0x41)       // body ptr
		code = append(code, encodeLEB128i(int32(bodyOffset))...)
		code = append(code, 0x41) // body size
		code = append(code, encodeLEB128i(int32(len(body)))...)
		code = append(code, 0x41, 0x00) // headers ptr = 0
		code = append(code, 0x41, 0x00) // headers size = 0
		code = append(code, 0x41, 0x7f) // grpc_status = -1
		code = append(code, 0x10, 0x00) // call func 0 (imported)
		code = append(code, 0x1a)       // drop result
		// return Continue
		code = append(code, 0x41)
		code = append(code, encodeLEB128i(int32(spec.onRequestHeadersAction))...)
		code = append(code, 0x0b)
		codes = append(codes, encodeFunc(code))

		// Data section: body string at offset 2048
		m.dataSegments = append(m.dataSegments, dataSegment{offset: bodyOffset, data: body})
	} else {
		codes = append(codes, encodeFunc([]byte{
			0x41, byte(spec.onRequestHeadersAction),
			0x0b,
		}))
	}

	m.section(10, encodeCodeSection(codes))

	// Data section (if any)
	if len(m.dataSegments) > 0 {
		m.section(11, encodeDataSection(m.dataSegments))
	}

	return m.bytes()
}

// ===== wasm binary encoding helpers =====

type wasmBuilder struct {
	buf          []byte
	dataSegments []dataSegment
}

type dataSegment struct {
	offset int
	data   []byte
}

func (w *wasmBuilder) init() {
	w.buf = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // magic + version
}

func (w *wasmBuilder) section(id byte, content []byte) {
	w.buf = append(w.buf, id)
	w.buf = append(w.buf, encodeULEB128(uint32(len(content)))...)
	w.buf = append(w.buf, content...)
}

func (w *wasmBuilder) bytes() []byte {
	return w.buf
}

type exportEntry struct {
	name  string
	kind  byte
	index uint32
}

func encodeExports(entries []exportEntry) []byte {
	var buf []byte
	buf = append(buf, encodeULEB128(uint32(len(entries)))...)
	for _, e := range entries {
		buf = append(buf, encodeULEB128(uint32(len(e.name)))...)
		buf = append(buf, []byte(e.name)...)
		buf = append(buf, e.kind)
		buf = append(buf, encodeULEB128(e.index)...)
	}
	return buf
}

func encodeImportSection(module, name string, typeIdx int) []byte {
	var buf []byte
	buf = append(buf, 1) // 1 import
	buf = append(buf, encodeULEB128(uint32(len(module)))...)
	buf = append(buf, []byte(module)...)
	buf = append(buf, encodeULEB128(uint32(len(name)))...)
	buf = append(buf, []byte(name)...)
	buf = append(buf, 0x00) // kind = func
	buf = append(buf, encodeULEB128(uint32(typeIdx))...)
	return buf
}

func encodeFunc(body []byte) []byte {
	// func body = [local_count] + body
	content := append([]byte{0}, body...) // 0 locals
	var buf []byte
	buf = append(buf, encodeULEB128(uint32(len(content)))...)
	buf = append(buf, content...)
	return buf
}

func encodeCodeSection(codes [][]byte) []byte {
	var buf []byte
	buf = append(buf, encodeULEB128(uint32(len(codes)))...)
	for _, c := range codes {
		buf = append(buf, c...)
	}
	return buf
}

func encodeDataSection(segments []dataSegment) []byte {
	var buf []byte
	buf = append(buf, encodeULEB128(uint32(len(segments)))...)
	for _, seg := range segments {
		buf = append(buf, 0x00) // active, memory 0
		// i32.const offset, end
		buf = append(buf, 0x41)
		buf = append(buf, encodeLEB128i(int32(seg.offset))...)
		buf = append(buf, 0x0b)
		// data bytes
		buf = append(buf, encodeULEB128(uint32(len(seg.data)))...)
		buf = append(buf, seg.data...)
	}
	return buf
}

func encodeULEB128(v uint32) []byte {
	var buf []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			break
		}
	}
	return buf
}

func encodeLEB128i(v int32) []byte {
	var buf []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			buf = append(buf, b)
			break
		}
		buf = append(buf, b|0x80)
	}
	return buf
}

// ===== 实际测试 =====

func TestRuntimeNewClose(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderMapRoundTrip(t *testing.T) {
	h := http.Header{}
	h.Add("X-Multi", "a")
	h.Add("X-Multi", "b")
	h.Set("Accept", "text/plain")

	data := encodeHeaderMap(h)
	pairs := decodeHeaderMap(data)
	result := pairsToHeader(pairs)

	if result.Get("Accept") != "text/plain" {
		t.Errorf("Accept = %q", result.Get("Accept"))
	}
	multi := result.Values("X-Multi")
	if len(multi) != 2 {
		t.Errorf("X-Multi values = %v", multi)
	}
}

func TestEncodeULEB128(t *testing.T) {
	tests := []struct {
		val  uint32
		want []byte
	}{
		{0, []byte{0}},
		{1, []byte{1}},
		{127, []byte{127}},
		{128, []byte{0x80, 0x01}},
		{624485, []byte{0xe5, 0x8e, 0x26}},
	}
	for _, tt := range tests {
		got := encodeULEB128(tt.val)
		if len(got) != len(tt.want) {
			t.Errorf("encodeULEB128(%d) = %v, want %v", tt.val, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("encodeULEB128(%d) = %v, want %v", tt.val, got, tt.want)
				break
			}
		}
	}
}

func TestEncodeLEB128i(t *testing.T) {
	tests := []struct {
		val  int32
		want []byte
	}{
		{0, []byte{0}},
		{1, []byte{1}},
		{-1, []byte{0x7f}},
		{-128, []byte{0x80, 0x7f}},
		{403, []byte{0x93, 0x03}},
	}
	for _, tt := range tests {
		got := encodeLEB128i(tt.val)
		if len(got) != len(tt.want) {
			t.Errorf("encodeLEB128i(%d) = %v, want %v", tt.val, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("encodeLEB128i(%d)[%d] = %02x, want %02x", tt.val, i, got[i], tt.want[i])
				break
			}
		}
	}
}

// TestHTTPFilterContinue 测试 guest 返回 CONTINUE 时流量正常通过。
func TestHTTPFilterContinue(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	wasmBytes := buildMinimalGuest()
	mod, err := rt.Compile(ctx, wasmBytes)
	if err != nil {
		t.Fatal(err)
	}

	filter := HTTPFilter(mod, WithPoolSize(2))
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("downstream ok"))
	})
	handler := filter(downstream)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "downstream ok" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// TestHTTPFilterDeny 测试 guest 调用 send_local_response 短路时的行为。
func TestHTTPFilterDeny(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	wasmBytes := buildDenyGuest()
	mod, err := rt.Compile(ctx, wasmBytes)
	if err != nil {
		t.Fatal(err)
	}

	filter := HTTPFilter(mod, WithPoolSize(2))
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("downstream should not be called")
	})
	handler := filter(downstream)

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if w.Body.String() != "blocked by wasm" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// suppress unused import warning
var _ = binary.LittleEndian

// ======== Shared Data Tests ========

func TestSharedData(t *testing.T) {
	sd := newSharedDataStore()

	// 不存在的 key
	_, _, ok := sd.Get("missing")
	if ok {
		t.Fatal("expected not found")
	}

	// 无条件写入(cas=0)
	if !sd.Set("key1", []byte("value1"), 0) {
		t.Fatal("set with cas=0 should succeed")
	}
	data, cas, ok := sd.Get("key1")
	if !ok || string(data) != "value1" || cas != 1 {
		t.Fatalf("get = %q, cas=%d, ok=%v", data, cas, ok)
	}

	// CAS 匹配写入
	if !sd.Set("key1", []byte("value2"), 1) {
		t.Fatal("set with matching cas should succeed")
	}
	data, cas, _ = sd.Get("key1")
	if string(data) != "value2" || cas != 2 {
		t.Fatalf("after CAS update: %q, cas=%d", data, cas)
	}

	// CAS 不匹配
	if sd.Set("key1", []byte("value3"), 999) {
		t.Fatal("set with wrong cas should fail")
	}
}

// ======== Shared Queue Tests ========

func TestSharedQueue(t *testing.T) {
	sq := newSharedQueue()

	// Register
	id := sq.Register("vm1", "my-queue")
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Resolve
	resolved, ok := sq.Resolve("vm1", "my-queue")
	if !ok || resolved != id {
		t.Fatalf("resolve: id=%d, ok=%v", resolved, ok)
	}

	// Resolve missing
	_, ok = sq.Resolve("vm1", "other")
	if ok {
		t.Fatal("expected not found")
	}

	// Enqueue/Dequeue
	if !sq.Enqueue(id, []byte("msg1")) {
		t.Fatal("enqueue failed")
	}
	if !sq.Enqueue(id, []byte("msg2")) {
		t.Fatal("enqueue failed")
	}
	if sq.Len(id) != 2 {
		t.Fatalf("len = %d", sq.Len(id))
	}

	msg, ok := sq.Dequeue(id)
	if !ok || string(msg) != "msg1" {
		t.Fatalf("dequeue: %q, ok=%v", msg, ok)
	}
	msg, ok = sq.Dequeue(id)
	if !ok || string(msg) != "msg2" {
		t.Fatalf("dequeue: %q, ok=%v", msg, ok)
	}

	// Empty queue
	_, ok = sq.Dequeue(id)
	if ok {
		t.Fatal("expected empty")
	}
}

// ======== Metrics Tests ========

func TestMetricStore(t *testing.T) {
	ms := newMetricStore()

	// Define
	id := ms.Define(0, "requests_total")
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Increment
	if !ms.Increment(id, 5) {
		t.Fatal("increment failed")
	}
	val, ok := ms.Get(id)
	if !ok || val != 5 {
		t.Fatalf("get = %d, ok=%v", val, ok)
	}

	// Record (overwrite)
	if !ms.Record(id, 100) {
		t.Fatal("record failed")
	}
	val, _ = ms.Get(id)
	if val != 100 {
		t.Fatalf("after record = %d", val)
	}

	// Snapshot
	snap := ms.Snapshot()
	if snap["requests_total"] != 100 {
		t.Fatalf("snapshot = %v", snap)
	}

	// Not found
	if _, ok := ms.Get(999); ok {
		t.Fatal("expected not found")
	}
}

// ======== Foreign Function Tests ========

func TestForeignFuncRegistry(t *testing.T) {
	ff := newForeignFuncRegistry()

	ff.Register("echo", func(args []byte) ([]byte, error) {
		return append([]byte("echo:"), args...), nil
	})

	result, err := ff.Call("echo", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "echo:hello" {
		t.Fatalf("result = %q", result)
	}

	// Not found
	_, err = ff.Call("missing", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
