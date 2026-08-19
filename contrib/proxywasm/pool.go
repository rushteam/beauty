package proxywasm

import "context"

// Pool 是有状态的 Proxy-Wasm 实例池。
// 与 contrib/wasm 的无状态池不同:这里的实例已完成 VM start + Plugin configure,
// 可以直接接收新的 stream context 处理请求。
type Pool struct {
	mod          *Module
	idle         chan *instance
	vmConfig     []byte
	pluginConfig []byte
	logLevel     LogLevel
	properties   map[string][]byte
}

// newPool 创建实例池。
func newPool(mod *Module, maxIdle int, vmConfig, pluginConfig []byte, logLevel LogLevel, props map[string][]byte) *Pool {
	if maxIdle < 1 {
		maxIdle = 1
	}
	return &Pool{
		mod:          mod,
		idle:         make(chan *instance, maxIdle),
		vmConfig:     vmConfig,
		pluginConfig: pluginConfig,
		logLevel:     logLevel,
		properties:   props,
	}
}

// Get 取一个已初始化的实例:有空闲则复用,否则新建并初始化。
func (p *Pool) Get(ctx context.Context) (*instance, error) {
	select {
	case inst := <-p.idle:
		return inst, nil
	default:
		return p.createInstance(ctx)
	}
}

// Put 归还实例。
func (p *Pool) Put(ctx context.Context, inst *instance) {
	select {
	case p.idle <- inst:
	default:
		_ = inst.close(ctx)
	}
}

// Warm 预建实例。
func (p *Pool) Warm(ctx context.Context, n int) error {
	capN := cap(p.idle)
	if n > capN {
		n = capN
	}
	need := n - len(p.idle)
	for i := 0; i < need; i++ {
		inst, err := p.createInstance(ctx)
		if err != nil {
			return err
		}
		select {
		case p.idle <- inst:
		default:
			_ = inst.close(ctx)
			return nil
		}
	}
	return nil
}

// Close 关闭所有空闲实例。
func (p *Pool) Close(ctx context.Context) {
	for {
		select {
		case inst := <-p.idle:
			_ = inst.close(ctx)
		default:
			return
		}
	}
}

func (p *Pool) createInstance(ctx context.Context) (*instance, error) {
	raw, err := p.mod.instantiate(ctx)
	if err != nil {
		return nil, err
	}
	rt := p.mod.rt
	return initInstance(ctx, raw.mod, p.vmConfig, p.pluginConfig, p.logLevel, p.properties, rt.sharedData, rt.metrics, rt.sharedQueue, rt.foreignFuncs, rt.dispatcher)
}
