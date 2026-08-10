package kitex

import (
	"context"
	"strconv"

	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	beautydiscover "github.com/rushteam/beauty/pkg/service/discover"
)

// ResolverAdapter 将 beauty 的 discover.Discovery 适配为 Kitex 的 discovery.Resolver。
// 使 Kitex 客户端能通过 beauty 注册中心发现服务。
//
// 使用方式：
//
//	adapter := kitex.NewResolverAdapter(beautyDiscovery)
//	client := itemservice.NewClient("example.shop.item",
//	    client.WithResolver(adapter),
//	)
type ResolverAdapter struct {
	discovery beautydiscover.Discovery
}

// NewResolverAdapter 创建 beauty.Discovery → kitex.Resolver 适配器。
func NewResolverAdapter(d beautydiscover.Discovery) discovery.Resolver {
	return &ResolverAdapter{discovery: d}
}

// Target 返回服务名作为缓存 key。
func (r *ResolverAdapter) Target(_ context.Context, target rpcinfo.EndpointInfo) string {
	return target.ServiceName()
}

// Resolve 调用 beauty.Discovery.Find 获取实例列表，转换为 Kitex discovery.Instance。
func (r *ResolverAdapter) Resolve(ctx context.Context, key string) (discovery.Result, error) {
	services, err := r.discovery.Find(ctx, key)
	if err != nil {
		return discovery.Result{}, err
	}

	instances := make([]discovery.Instance, 0, len(services))
	for _, svc := range services {
		weight := 100
		if w, ok := svc.Metadata["weight"]; ok {
			if v, err := strconv.Atoi(w); err == nil && v > 0 {
				weight = v
			}
		}
		instances = append(instances, discovery.NewInstance(
			"tcp",
			svc.Addr,
			weight,
			svc.Metadata,
		))
	}

	return discovery.Result{
		Cacheable: true,
		CacheKey:  key,
		Instances: instances,
	}, nil
}

// Diff 使用 Kitex 内置的 DefaultDiff 计算实例列表变更。
func (r *ResolverAdapter) Diff(cacheKey string, prev, next discovery.Result) (discovery.Change, bool) {
	return discovery.DefaultDiff(cacheKey, prev, next)
}

// Name 返回 resolver 名称。
func (r *ResolverAdapter) Name() string {
	return "beauty-discovery"
}
