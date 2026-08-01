// Package wasmagent 是 beauty 的 wasm × agent 胶水模块:把 contrib/wasm 的沙箱执行能力接到
// contrib/llm/agent 的工具循环上——
//
//   - NewWasmExecutor:把 wasm 执行器适配成 skills.ScriptExecutor,让 skills 的脚本在 wazero
//     沙箱内运行(替代 EnableExec 的本地进程执行)。此时技能的 scripts/ 里放的是 **.wasm 模块**,
//     按本包的 alloc/handle ABI 接收参数、返回输出文本;
//   - ToolFrom:直接把一个 wasm 模块包装成 agent.Tool(模型调用 → wasm handle → 结果文本)。
//
// 这是刻意独立的胶水模块:它同时依赖 wasm 与 llm/agent,好让那两个模块彼此零耦合。
//
// guest ABI(与 contrib/wasm 中间件同一套):
//   - 导出 memory;alloc(i32 size)->i32 ptr;handle(i32 argsPtr, i32 argsLen)->i64,
//     返回 (respPtr<<32)|respLen,内容为**纯文本输出**(直接喂回模型)。
//   - 输入 JSON:{"args":[...], "cwd":"技能目录"}。
package wasmagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/skills"
	"github.com/rushteam/beauty/contrib/wasm"
	"github.com/tetratelabs/wazero/api"
)

// Executor 把 wasm 沙箱执行适配成 skills.ScriptExecutor:技能的 scripts/ 里放 .wasm 模块,
// 执行时读文件→编译(按 path+mtime 缓存)→实例化(实例池)→handle(args)→输出文本。
// 并发安全。
type Executor struct {
	rt      *wasm.Runtime
	timeout time.Duration
	poolSz  int
	allocFn string
	handle  string

	mu     sync.Mutex
	byPath map[string]*cachedMod
}

type cachedMod struct {
	mod   *wasm.Module
	pool  *wasm.Pool
	mtime time.Time
}

// ExecutorOption 配置 Executor。
type ExecutorOption func(*Executor)

// WithTimeout 设置单次 wasm 执行超时(<=0 用 30s)。超时中断 guest(依赖 Runtime 的 CloseOnContextDone)。
func WithTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) { e.timeout = d }
}

// WithPool 设置每个已编译模块保留的空闲实例数(<=0 用 2)。复用实例摊薄实例化开销。
func WithPool(size int) ExecutorOption { return func(e *Executor) { e.poolSz = size } }

// WithFuncNames 覆盖 guest 导出函数名(默认 "alloc"/"handle")。
func WithFuncNames(alloc, handle string) ExecutorOption {
	return func(e *Executor) { e.allocFn, e.handle = alloc, handle }
}

// NewWasmExecutor 创建 wasm 脚本执行器。rt 用 contrib/wasm.New 构造(可配内存上限/host funcs)。
// 返回的 Executor 实现了 skills.ScriptExecutor,直接:
//
//	skills.Load(...).WithScriptExecutor(wasmagent.NewWasmExecutor(rt)).EnableExec(30*time.Second)
func NewWasmExecutor(rt *wasm.Runtime, opts ...ExecutorOption) *Executor {
	e := &Executor{
		rt: rt, timeout: 30 * time.Second, poolSz: 2,
		allocFn: "alloc", handle: "handle",
		byPath: map[string]*cachedMod{},
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// 编译缓存接口(便于 skills.WithScriptExecutor 注入)。实现 skills.ScriptExecutor 签名。
func (e *Executor) Exec(ctx context.Context, path, cwd string, args []string) (string, error) {
	cm, err := e.module(ctx, path)
	if err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	inst, err := cm.pool.Get(cctx)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
	}{Args: args, Cwd: cwd})
	if err != nil {
		_ = inst.Close(context.Background())
		return "", err
	}
	out, err := e.runOnce(cctx, inst, payload)
	if err == nil {
		cm.pool.Put(context.Background(), inst)
	} else {
		_ = inst.Close(context.Background()) // 出错/超时被中断的实例丢弃
	}
	return out, err
}

// module 按 path+mtime 编译并缓存 wasm 模块(文件变更后重编译)。
func (e *Executor) module(ctx context.Context, path string) (*cachedMod, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("wasmagent: stat %s: %w", path, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if cm, ok := e.byPath[path]; ok && cm.mtime.Equal(st.ModTime()) {
		return cm, nil
	}
	if old, ok := e.byPath[path]; ok {
		old.pool.Close(ctx)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("wasmagent: 读 %s: %w", path, err)
	}
	mod, err := e.rt.Compile(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("wasmagent: 编译 %s: %w", path, err)
	}
	cm := &cachedMod{mod: mod, pool: mod.NewPool(e.poolSz), mtime: st.ModTime()}
	e.byPath[path] = cm
	return cm, nil
}

// runOnce 在实例上执行一次:写输入 payload→handle→读输出文本。payload 的 JSON 结构由调用方定
// (skills 执行器传 {"args":[...],"cwd":...};ToolFrom 直接传模型给的 args)。
func (e *Executor) runOnce(ctx context.Context, inst *wasm.Instance, payload []byte) (string, error) {
	ptr, err := inst.WriteTo(ctx, e.allocFn, payload)
	if err != nil {
		return "", err
	}
	res, err := inst.Call(ctx, e.handle, api.EncodeU32(ptr), api.EncodeU32(uint32(len(payload))))
	if err != nil {
		return "", err
	}
	if len(res) == 0 {
		return "", fmt.Errorf("wasmagent: handle 无返回")
	}
	packed := res[0]
	out, err := inst.ReadBytes(uint32(packed>>32), uint32(packed))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// 编译期断言:Executor 满足 skills.ScriptExecutor。
var _ skills.ScriptExecutor = (*Executor)(nil).Exec

// ToolFrom 把一个 wasm 模块包装成 agent.Tool:模型调用时,args(JSON)原样传给 wasm handle,
// 返回的输出文本喂回模型。mod 由 rt.Compile 得到;每次调用用模块的实例池取实例。
// Approval 语义由调用方在 agent.Tool 上自行设置(见 agent.Tool.Approval)。
func ToolFrom(mod *wasm.Module, name, description string, parameters json.RawMessage, opts ...ExecutorOption) agent.Tool {
	e := &Executor{
		timeout: 30 * time.Second, poolSz: 2,
		allocFn: "alloc", handle: "handle",
	}
	for _, o := range opts {
		o(e)
	}
	pool := mod.NewPool(e.poolSz)
	return agent.Tool{
		Def: llm.ToolDef{Name: name, Description: description, Parameters: parameters},
		Call: func(ctx context.Context, args json.RawMessage) (string, error) {
			cctx, cancel := context.WithTimeout(ctx, e.timeout)
			defer cancel()
			inst, err := pool.Get(cctx)
			if err != nil {
				return "", err
			}
			out, err := e.runOnce(cctx, inst, []byte(args))
			if err == nil {
				pool.Put(context.Background(), inst)
			} else {
				_ = inst.Close(context.Background())
			}
			return out, err
		},
	}
}
