package registry

import (
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/service/discover/nacos"
	"{{.ImportPath}}internal/config"
)

// New 创建服务注册器
func New(cfg *config.Config) discover.Registry {
	switch cfg.Registry.Type {
	case "etcd":
		return etcdv3.NewRegistry(&etcdv3.Config{
			Endpoints: cfg.Registry.Endpoints,
			Prefix:    cfg.Registry.Config["prefix"],
		})
	case "nacos":
		return nacos.NewRegistry(&nacos.Config{
			Endpoints: cfg.Registry.Endpoints,
			Namespace: cfg.Registry.Config["namespace"],
			Group:     cfg.Registry.Config["group"],
		})
	default:
		return discover.NewNoopRegistry()
	}
}
