package mdrt

import "context"

// Build 编译 spec 目录,跑完全部例子,把脚本、锁文件和缓存写到磁盘。
// 不启动 HTTP。缓存缺失且没有 WithLLM 时返回错误。
func Build(ctx context.Context, opts ...Option) (*Runtime, error) {
	all := make([]Option, 0, len(opts)+1)
	all = append(all, opts...)
	all = append(all, WithMode(ModeBuild))
	rt, err := New(all...)
	if err != nil {
		return nil, err
	}
	batch, err := rt.readAll(ctx, produceFlags{sync: rt.cfg.llm != nil, strict: rt.cfg.llm == nil})
	if err != nil {
		return rt, err
	}
	if err := rt.apply(ctx, batch, applyOpt{strict: true}); err != nil {
		return rt, err
	}
	if vm := rt.live.Load(); vm != nil {
		vm.Close()
	}
	return rt, nil
}
