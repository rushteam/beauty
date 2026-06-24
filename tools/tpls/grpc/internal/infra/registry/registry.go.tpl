package registry

import (
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/discover/consul"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/service/discover/k8s"
	"github.com/rushteam/beauty/pkg/service/discover/nacos"
	"github.com/rushteam/beauty/pkg/service/discover/polaris"
	"{{.ImportPath}}internal/config"
)

// New 根据配置创建服务注册器。
// 支持的 registry.type: etcd / nacos / consul / polaris / k8s，
// 其余值（含空）返回 Noop（不注册）。
func New(cfg *config.Config) discover.Registry {
	switch cfg.Registry.Type {
	case "etcd":
		return etcdv3.NewRegistry(&etcdv3.Config{
			Endpoints: cfg.Registry.Endpoints,
			Prefix:    cfg.Registry.Config["prefix"],
			TTL:       10,
		})
	case "nacos":
		return nacos.NewRegistry(&nacos.Config{
			Addr:      cfg.Registry.Endpoints,
			Namespace: cfg.Registry.Config["namespace"],
			Group:     cfg.Registry.Config["group"],
		})
	case "consul":
		addr := ""
		if len(cfg.Registry.Endpoints) > 0 {
			addr = cfg.Registry.Endpoints[0]
		}
		return consul.NewRegistry(&consul.Config{
			Addr:       addr,
			Token:      cfg.Registry.Config["token"],
			Namespace:  cfg.Registry.Config["namespace"],
			Datacenter: cfg.Registry.Config["datacenter"],
		})
	case "polaris":
		return polaris.NewRegistry(&polaris.Config{
			Addresses: cfg.Registry.Endpoints,
			Namespace: cfg.Registry.Config["namespace"],
			Token:     cfg.Registry.Config["token"],
			Service:   cfg.Registry.Config["service"],
		})
	case "k8s":
		return k8s.NewRegistry(&k8s.Config{
			Kubeconfig: cfg.Registry.Config["kubeconfig"],
			Namespace:  cfg.Registry.Config["namespace"],
		})
	default:
		return discover.NewNoop()
	}
}
