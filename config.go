package beauty

import (
	"context"

	"github.com/rushteam/beauty/pkg/conf"
	"github.com/rushteam/beauty/pkg/service/logger"
)

// WithConfig 把配置接入应用生命周期：构造时用 loader 首次加载并回调 onChange，
// 随后随应用运行 Watch 配置变更、每次变更重新加载并回调；应用关闭时停止 Watch。
//
// onChange 收到反序列化好的最新配置（每次都是新实例），在其中应用即可：
//
//	loader, _ := conf.New("etcd://127.0.0.1:2379/app/config.yaml")
//	var current atomic.Pointer[AppConfig]
//	app := beauty.New(
//	    beauty.WithConfig(loader, func(c *AppConfig) { current.Store(c) }),
//	    beauty.WithWebServer(":8080", mux),
//	)
//
// 远程坏配置不会覆盖上一份可用值（见 pkg/conf 的先校验后提交）。
func WithConfig[T any](loader conf.Loader, onChange func(cfg *T)) Option {
	return WithComponent(&configComponent[T]{loader: loader, onChange: onChange})
}

type configComponent[T any] struct {
	loader   conf.Loader
	onChange func(*T)
}

func (c *configComponent[T]) Name() string { return "config" }

func (c *configComponent[T]) Init() context.CancelFunc {
	c.reload() // 初始加载
	ctx, cancel := context.WithCancel(context.Background())
	c.loader.Watch(ctx, c.reload)
	return cancel
}

func (c *configComponent[T]) reload() {
	var cfg T
	if err := c.loader.Unmarshal(&cfg); err != nil {
		logger.Error("config: unmarshal failed", "error", err)
		return
	}
	if c.onChange != nil {
		c.onChange(&cfg)
	}
}
