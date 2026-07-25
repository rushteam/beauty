// Package wasmopa 用 OPA 编译出的 wasm 策略模块实现 beauty 的 authz.Enforcer——
// 把 Rego 策略编译成 wasm,在 wazero 沙箱里执行,实现"策略即 wasm":
//
//	opa build -t wasm -e 'authz/allow' policy.rego → bundle 里的 policy.wasm
//	→ wasmopa.New(wasmBytes, opts...) → authz.Enforcer
//
// 这是纯 Go 的 OPA 策略求值(基于 wazero,无 CGo、无外部进程);
// 比引入完整 OPA SDK 轻量得多,只需 wasm 模块。
//
// 两层 API:
//   - Policy.Eval(ctx, input):通用策略求值,返回 JSON 结果——可用于 governance(熔断/路由策略)
//     或任意 Rego 决策;
//   - Policy.Authorize(ctx, sub, action, resource):实现 authz.Enforcer,input 自动构造为
//     {"subject":...,"action":...,"resource":...},判定 result[0].result.allow == true。
//
// OPA wasm ABI 1.2+(使用 opa_eval 一次性求值函数)。实例池化、并发安全。
package wasmopa

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/authz"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Policy 封装一个 OPA 编译出的 wasm 策略模块,并发安全。
// 每个并发求值槽位持有独立的 wazero Runtime(含独立 memory),避免共享内存竞争。
type Policy struct {
	wasmBytes []byte
	timeout   time.Duration
	poolSz    int

	mu       sync.RWMutex
	dataJSON []byte
	epID     int32

	instMu sync.Mutex
	pool   []*opaInstance
}

type opaInstance struct {
	rt  wazero.Runtime
	mod api.Module
}

// Option 配置 Policy。
type Option func(*Policy)

// WithTimeout 设置单次求值超时(<=0 用 5s)。
func WithTimeout(d time.Duration) Option { return func(p *Policy) { p.timeout = d } }

// WithPool 设置空闲实例池大小(<=0 用 4)。
func WithPool(size int) Option {
	return func(p *Policy) {
		if size > 0 {
			p.poolSz = size
		}
	}
}

// WithData 预设 OPA 外部数据(data document)。data 为 JSON 对象;nil/空表示空对象。
func WithData(data json.RawMessage) Option {
	return func(p *Policy) { p.dataJSON = normalizeData(data) }
}

// WithEntrypoint 指定 entrypoint ID(多入口策略时使用;默认 0)。
func WithEntrypoint(id int32) Option { return func(p *Policy) { p.epID = id } }

// New 编译 OPA wasm 模块并创建 Policy。wasmBytes 由 `opa build -t wasm` 生成的 policy.wasm。
func New(wasmBytes []byte, opts ...Option) (*Policy, error) {
	// 先验证一次能否编译+实例化
	inst, err := newOPAInstance(wasmBytes)
	if err != nil {
		return nil, err
	}

	p := &Policy{
		wasmBytes: wasmBytes,
		timeout:   5 * time.Second,
		poolSz:    4,
		dataJSON:  []byte("{}"),
		pool:      []*opaInstance{inst},
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// newOPAInstance 创建一个独立的 OPA 求值实例(独立 Runtime + env + policy module)。
func newOPAInstance(wasmBytes []byte) (*opaInstance, error) {
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)

	if _, err := rt.InstantiateModule(ctx, mustCompile(ctx, rt, buildEnvModule()),
		wazero.NewModuleConfig().WithName("env")); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasmopa: env module: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasmopa: compile: %w", err)
	}

	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasmopa: instantiate: %w", err)
	}

	return &opaInstance{rt: rt, mod: mod}, nil
}

func mustCompile(ctx context.Context, rt wazero.Runtime, wasm []byte) wazero.CompiledModule {
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		panic("wasmopa: compile env module: " + err.Error())
	}
	return mod
}

// buildEnvModule 构造一个最小 wasm 模块,导出:
//   - memory(初始 2 页,最大 256 页)
//   - opa_abort(i32) → void (nop)
//   - opa_println(i32) → void (nop)
//   - opa_builtin0(i32,i32) → i32 (返回 0)
//   - opa_builtin1(i32,i32,i32) → i32 (返回 0)
//   - opa_builtin2(i32,i32,i32,i32) → i32 (返回 0)
//   - opa_builtin3(i32,i32,i32,i32,i32) → i32 (返回 0)
//   - opa_builtin4(i32,i32,i32,i32,i32,i32) → i32 (返回 0)
func buildEnvModule() []byte {
	const (
		valI32  = 0x7f
		secType = 1
		secFunc = 3
		secMem  = 5
		secExpt = 7
		secCode = 10
	)

	// helper encoders
	uleb := func(v uint32) []byte {
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
	vec := func(items ...[]byte) []byte {
		out := uleb(uint32(len(items)))
		for _, it := range items {
			out = append(out, it...)
		}
		return out
	}
	sec := func(id byte, payload []byte) []byte {
		return append([]byte{id}, append(uleb(uint32(len(payload))), payload...)...)
	}
	funcType := func(params, results []byte) []byte {
		out := []byte{0x60}
		out = append(out, append(uleb(uint32(len(params))), params...)...)
		return append(out, append(uleb(uint32(len(results))), results...)...)
	}
	exportEntry := func(name string, kind byte, idx uint32) []byte {
		out := append(uleb(uint32(len(name))), []byte(name)...)
		out = append(out, kind)
		return append(out, uleb(idx)...)
	}
	codeEntry := func(body []byte) []byte {
		b := append([]byte{0x00}, body...) // 0 locals
		return append(uleb(uint32(len(b))), b...)
	}

	// Types: 7 function types
	// t0: (i32) → ()         opa_abort, opa_println
	// t1: (i32,i32) → (i32)  opa_builtin0
	// t2: (i32,i32,i32) → (i32)  opa_builtin1
	// t3: (i32,i32,i32,i32) → (i32)  opa_builtin2
	// t4: (i32,i32,i32,i32,i32) → (i32)  opa_builtin3
	// t5: (i32,i32,i32,i32,i32,i32) → (i32)  opa_builtin4
	t0 := funcType([]byte{valI32}, nil)
	t1 := funcType([]byte{valI32, valI32}, []byte{valI32})
	t2 := funcType([]byte{valI32, valI32, valI32}, []byte{valI32})
	t3 := funcType([]byte{valI32, valI32, valI32, valI32}, []byte{valI32})
	t4 := funcType([]byte{valI32, valI32, valI32, valI32, valI32}, []byte{valI32})
	t5 := funcType([]byte{valI32, valI32, valI32, valI32, valI32, valI32}, []byte{valI32})

	nop := []byte{0x0b}                         // end (nop for void funcs)
	retZero := []byte{0x41, 0x00, 0x0b}         // i32.const 0; end

	m := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // magic + version

	// Section 1: Type
	m = append(m, sec(secType, vec(t0, t1, t2, t3, t4, t5))...)

	// Section 3: Function (7 funcs: abort, println, builtin0-4)
	m = append(m, sec(secFunc, vec(
		uleb(0), uleb(0), // abort, println: type 0
		uleb(1), // builtin0: type 1
		uleb(2), // builtin1: type 2
		uleb(3), // builtin2: type 3
		uleb(4), // builtin3: type 4
		uleb(5), // builtin4: type 5
	))...)

	// Section 5: Memory (1 memory: min 2, max 256)
	memPayload := append(uleb(1), 0x01) // 1 memory, has-max flag
	memPayload = append(memPayload, uleb(2)...)
	memPayload = append(memPayload, uleb(256)...)
	m = append(m, sec(secMem, memPayload)...)

	// Section 7: Export
	m = append(m, sec(secExpt, vec(
		exportEntry("memory", 0x02, 0),       // memory export
		exportEntry("opa_abort", 0x00, 0),    // func 0
		exportEntry("opa_println", 0x00, 1),  // func 1
		exportEntry("opa_builtin0", 0x00, 2), // func 2
		exportEntry("opa_builtin1", 0x00, 3), // func 3
		exportEntry("opa_builtin2", 0x00, 4), // func 4
		exportEntry("opa_builtin3", 0x00, 5), // func 5
		exportEntry("opa_builtin4", 0x00, 6), // func 6
	))...)

	// Section 10: Code
	m = append(m, sec(secCode, vec(
		codeEntry(nop),     // abort: nop
		codeEntry(nop),     // println: nop
		codeEntry(retZero), // builtin0: i32.const 0
		codeEntry(retZero), // builtin1: i32.const 0
		codeEntry(retZero), // builtin2: i32.const 0
		codeEntry(retZero), // builtin3: i32.const 0
		codeEntry(retZero), // builtin4: i32.const 0
	))...)

	return m
}

// Close 关闭 Policy 及所有缓存的 wazero Runtime。
func (p *Policy) Close() error {
	p.instMu.Lock()
	for _, inst := range p.pool {
		inst.rt.Close(context.Background())
	}
	p.pool = nil
	p.instMu.Unlock()
	return nil
}

// SetData 运行时更新外部数据(热加载)。并发安全。
func (p *Policy) SetData(data json.RawMessage) {
	p.mu.Lock()
	p.dataJSON = normalizeData(data)
	p.mu.Unlock()
}

// Eval 通用策略求值:以 input(任意 JSON)调用 opa_eval,返回结果 JSON。
func (p *Policy) Eval(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	inst, err := p.getInstance(cctx)
	if err != nil {
		return nil, err
	}

	result, err := p.eval(cctx, inst, input)
	if err != nil {
		inst.rt.Close(context.Background())
		return nil, err
	}
	p.putInstance(inst)
	return result, nil
}

// Authorize 实现 authz.Enforcer。
func (p *Policy) Authorize(ctx context.Context, sub authz.Subject, action, resource string) error {
	input, err := json.Marshal(authzInput{
		Subject:  authzSubject{ID: sub.ID, Roles: sub.Roles, Attrs: sub.Attrs},
		Action:   action,
		Resource: resource,
	})
	if err != nil {
		return fmt.Errorf("wasmopa: marshal input: %w", err)
	}

	result, err := p.Eval(ctx, input)
	if err != nil {
		return err
	}

	allowed, err := extractAllow(result)
	if err != nil {
		return err
	}
	if !allowed {
		return authz.ErrDenied
	}
	return nil
}

// --- 实例池 ---

func (p *Policy) getInstance(ctx context.Context) (*opaInstance, error) {
	p.instMu.Lock()
	if len(p.pool) > 0 {
		inst := p.pool[len(p.pool)-1]
		p.pool = p.pool[:len(p.pool)-1]
		p.instMu.Unlock()
		return inst, nil
	}
	p.instMu.Unlock()

	return newOPAInstance(p.wasmBytes)
}

func (p *Policy) putInstance(inst *opaInstance) {
	p.instMu.Lock()
	if len(p.pool) < p.poolSz {
		p.pool = append(p.pool, inst)
		p.instMu.Unlock()
		return
	}
	p.instMu.Unlock()
	inst.rt.Close(context.Background())
}

// --- OPA eval 协议 ---

func (p *Policy) eval(ctx context.Context, inst *opaInstance, input json.RawMessage) (json.RawMessage, error) {
	mod := inst.mod

	p.mu.RLock()
	dataJSON := p.dataJSON
	p.mu.RUnlock()

	// 1. 保存 heap 指针(求值后重置回此处,回收本次分配)
	baseHeap, err := callU32(ctx, mod, "opa_heap_ptr_get")
	if err != nil {
		return nil, fmt.Errorf("wasmopa: opa_heap_ptr_get: %w", err)
	}

	// 2. 写 data → malloc → json_parse
	dataAddr, err := writeAndParse(ctx, mod, dataJSON)
	if err != nil {
		return nil, fmt.Errorf("wasmopa: parse data: %w", err)
	}

	// 3. 写 input → malloc (opa_eval 接收 raw string)
	inputAddr, err := writeRaw(ctx, mod, input)
	if err != nil {
		return nil, fmt.Errorf("wasmopa: write input: %w", err)
	}

	// 4. 获取分配后的 heap 指针——传给 opa_eval 作为求值期间的堆起始位置
	evalHeap, err := callU32(ctx, mod, "opa_heap_ptr_get")
	if err != nil {
		return nil, fmt.Errorf("wasmopa: opa_heap_ptr_get(2): %w", err)
	}

	// 5. opa_eval(0, entrypoint, dataAddr, inputAddr, inputLen, heapPtr, 0=JSON)
	fn := mod.ExportedFunction("opa_eval")
	if fn == nil {
		return nil, fmt.Errorf("wasmopa: module 未导出 opa_eval(需要 ABI >=1.2)")
	}
	res, err := fn.Call(ctx,
		0, // reserved
		api.EncodeI32(p.epID),
		uint64(dataAddr),
		uint64(inputAddr),
		uint64(len(input)),
		uint64(evalHeap),
		0, // format: JSON
	)
	if err != nil {
		return nil, fmt.Errorf("wasmopa: opa_eval: %w", err)
	}
	resultAddr := uint32(res[0])

	// 6. 读 NUL-terminated JSON 结果
	resultBytes, err := readCString(mod, resultAddr)
	if err != nil {
		return nil, fmt.Errorf("wasmopa: read result: %w", err)
	}

	// 7. 重置 heap 到基础位置(回收所有本次求值的分配)
	if setFn := mod.ExportedFunction("opa_heap_ptr_set"); setFn != nil {
		_, _ = setFn.Call(ctx, uint64(baseHeap))
	}

	return json.RawMessage(resultBytes), nil
}

func callU32(ctx context.Context, mod api.Module, name string) (uint32, error) {
	fn := mod.ExportedFunction(name)
	if fn == nil {
		return 0, fmt.Errorf("未导出 %s", name)
	}
	res, err := fn.Call(ctx)
	if err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return uint32(res[0]), nil
}

func writeRaw(ctx context.Context, mod api.Module, data []byte) (uint32, error) {
	if len(data) == 0 {
		data = []byte("{}")
	}
	mallocFn := mod.ExportedFunction("opa_malloc")
	if mallocFn == nil {
		return 0, fmt.Errorf("未导出 opa_malloc")
	}
	res, err := mallocFn.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, err
	}
	addr := uint32(res[0])
	mem := mod.Memory()
	if !mem.Write(addr, data) {
		return 0, fmt.Errorf("写内存失败(addr=%d len=%d)", addr, len(data))
	}
	return addr, nil
}

func writeAndParse(ctx context.Context, mod api.Module, data []byte) (uint32, error) {
	addr, err := writeRaw(ctx, mod, data)
	if err != nil {
		return 0, err
	}
	parseFn := mod.ExportedFunction("opa_json_parse")
	if parseFn == nil {
		return 0, fmt.Errorf("未导出 opa_json_parse")
	}
	res, err := parseFn.Call(ctx, uint64(addr), uint64(len(data)))
	if err != nil {
		return 0, err
	}
	parsed := uint32(res[0])
	if parsed == 0 {
		return 0, fmt.Errorf("opa_json_parse 返回 NULL")
	}
	return parsed, nil
}

func readCString(mod api.Module, addr uint32) ([]byte, error) {
	mem := mod.Memory()
	const chunkSize = 4096
	const maxLen = 1 << 20
	var collected []byte
	for offset := uint32(0); offset < maxLen; offset += chunkSize {
		size := uint32(chunkSize)
		if memSize := mem.Size(); addr+offset+size > memSize {
			size = memSize - (addr + offset)
			if size == 0 {
				break
			}
		}
		buf, ok := mem.Read(addr+offset, size)
		if !ok {
			break
		}
		for i, b := range buf {
			if b == 0 {
				return append(collected, buf[:i]...), nil
			}
		}
		collected = append(collected, buf...)
	}
	if len(collected) > 0 {
		return collected, nil
	}
	return nil, fmt.Errorf("wasmopa: 读结果失败(addr=%d)", addr)
}

// --- 辅助类型 ---

type authzInput struct {
	Subject  authzSubject `json:"subject"`
	Action   string       `json:"action"`
	Resource string       `json:"resource"`
}

type authzSubject struct {
	ID    string            `json:"id"`
	Roles []string          `json:"roles"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

func extractAllow(result json.RawMessage) (bool, error) {
	// OPA entrypoint 结果格式: [{"result":true}] 或 [{"result":{"allow":true}}]
	// 对于 `opa build -e 'authz/allow'`(具体规则入口),结果是 [{"result":true/false}]
	var bindings []struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(result, &bindings); err != nil {
		return false, fmt.Errorf("wasmopa: 解析结果: %w (raw: %s)", err, result)
	}
	if len(bindings) == 0 {
		return false, nil
	}

	// 尝试直接解析为 bool(入口是具体规则 e.g. authz/allow)
	var b bool
	if json.Unmarshal(bindings[0].Result, &b) == nil {
		return b, nil
	}

	// 尝试解析为对象(入口是包 e.g. authz → {"allow":...})
	var obj struct {
		Allow bool `json:"allow"`
	}
	if json.Unmarshal(bindings[0].Result, &obj) == nil {
		return obj.Allow, nil
	}

	return false, nil
}

func normalizeData(data json.RawMessage) []byte {
	if len(data) == 0 {
		return []byte("{}")
	}
	return []byte(data)
}

// 编译期断言。
var _ authz.Enforcer = (*Policy)(nil)
