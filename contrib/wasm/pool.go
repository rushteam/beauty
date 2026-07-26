package wasm

import "context"

// Pool 是一个 wasm 实例的空闲free-list:复用实例以摊薄实例化开销(重运行时 guest 尤为明显)。
// 非阻塞:Get 优先取空闲实例、没有就新建;Put 放回,池满则关闭多余实例。因此并发实例数不设硬上限
// (每个在途请求各持一个),仅**保留的空闲实例数**受 maxIdle 限制。并发安全。
//
// 注意:实例被**复用**,其线性内存/全局状态会跨请求保留——guest 不能依赖"每次都是全新状态",
// 应在入口(如 handle)自行重置。出错/超时的实例不应放回(应 Close 丢弃)。
type Pool struct {
	mod  *Module
	idle chan *Instance
}

// NewPool 创建实例池,最多保留 maxIdle 个空闲实例(<1 视为 1)。
func (m *Module) NewPool(maxIdle int) *Pool {
	if maxIdle < 1 {
		maxIdle = 1
	}
	return &Pool{mod: m, idle: make(chan *Instance, maxIdle)}
}

// Get 取一个实例:有空闲则复用,否则新建。
func (p *Pool) Get(ctx context.Context) (*Instance, error) {
	select {
	case inst := <-p.idle:
		return inst, nil
	default:
		return p.mod.Instantiate(ctx)
	}
}

// Put 归还实例:空闲池未满则留存复用,已满则关闭(丢弃)。
func (p *Pool) Put(ctx context.Context, inst *Instance) {
	select {
	case p.idle <- inst:
	default:
		_ = inst.Close(ctx)
	}
}

// Warm 启动时预建最多 n 个空闲实例,摊掉首批请求的冷启动。n 超过池容量时按容量截断;
// 已有空闲实例时只补足差额。任一实例化失败则返回错误(已建的仍留在池中)。
func (p *Pool) Warm(ctx context.Context, n int) error {
	if n < 1 {
		return nil
	}
	capN := cap(p.idle)
	if n > capN {
		n = capN
	}
	need := n - len(p.idle)
	for i := 0; i < need; i++ {
		inst, err := p.mod.Instantiate(ctx)
		if err != nil {
			return err
		}
		select {
		case p.idle <- inst:
		default:
			_ = inst.Close(ctx)
			return nil
		}
	}
	return nil
}

// Idle 返回当前空闲实例数(瞬时值,仅供观测)。
func (p *Pool) Idle() int { return len(p.idle) }

// Close 关闭并清空所有空闲实例。在途实例由各自持有者负责关闭。
func (p *Pool) Close(ctx context.Context) {
	for {
		select {
		case inst := <-p.idle:
			_ = inst.Close(ctx)
		default:
			return
		}
	}
}
