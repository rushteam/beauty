// Package router 提供服务发现与负载均衡之间的路由过滤层。
//
// 客户端选实例的链路:服务发现(discover.Find)→ ServiceRouter.Filter(过滤)→
// 负载均衡(loadbalance.Next 选一个)→ 熔断(Available 检查)。把过滤从 selectService
// 内联逻辑里抽出来,未来加金丝雀/同 AZ 优先/灰度等路由策略只改 router,不动负载算法。
//
// LabelRouter 复用 pkg/utils/selector.LabelFilter 做标签路由(version/region/zone 等);
// ChainRouter 串联多个 router。所有 router 无状态(基于入参 nodes 计算),并发安全。
package router

import (
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

// ServiceRouter 服务路由器:在服务发现选出全部实例后、负载均衡选单个实例前过滤。
// Filter 返回过滤后的子集;返回空切片表示无匹配(调用方应 fail-closed 报错,不退回全量)。
type ServiceRouter interface {
	Filter(serviceName string, nodes []discover.ServiceInfo) []discover.ServiceInfo
}

// NoopRouter 原样返回所有节点。默认实现,未配置 router 时零开销。
type NoopRouter struct{}

// Filter 原样返回 nodes。
func (NoopRouter) Filter(_ string, nodes []discover.ServiceInfo) []discover.ServiceInfo {
	return nodes
}

// LabelRouter 基于 selector.LabelFilter 的标签路由。按 metadata 里的 version/region/zone
// 等标签过滤实例,用于灰度、地域亲和、版本路由等场景。
type LabelRouter struct {
	filter *selector.LabelFilter
}

// NewLabelRouter 创建标签路由。filter 为 nil 时等价 NoopRouter(不过滤)。
func NewLabelRouter(filter *selector.LabelFilter) *LabelRouter {
	return &LabelRouter{filter: filter}
}

// Filter 按 filter.Matches(node.Metadata) 过滤。filter 为 nil 或 nodes 为空时原样返回。
// 无匹配时返回空切片(由调用方 fail-closed 处理,不在此退回全量)。
func (r *LabelRouter) Filter(_ string, nodes []discover.ServiceInfo) []discover.ServiceInfo {
	if r.filter == nil || len(nodes) == 0 {
		return nodes
	}
	out := make([]discover.ServiceInfo, 0, len(nodes))
	for _, n := range nodes {
		if r.filter.Matches(n.Metadata) {
			out = append(out, n)
		}
	}
	return out
}

// ChainRouter 串联多个 ServiceRouter,前一个的输出喂给后一个。用于组合标签路由 + 自定义路由。
type ChainRouter struct {
	routers []ServiceRouter
}

// NewChainRouter 创建链式路由。按传入顺序执行。无参时等价 NoopRouter。
func NewChainRouter(routers ...ServiceRouter) *ChainRouter {
	return &ChainRouter{routers: routers}
}

// Filter 依次应用链上每个 router。
func (c *ChainRouter) Filter(serviceName string, nodes []discover.ServiceInfo) []discover.ServiceInfo {
	for _, r := range c.routers {
		nodes = r.Filter(serviceName, nodes)
		if len(nodes) == 0 {
			return nodes // 提前返回,fail-closed
		}
	}
	return nodes
}

var (
	_ ServiceRouter = NoopRouter{}
	_ ServiceRouter = (*LabelRouter)(nil)
	_ ServiceRouter = (*ChainRouter)(nil)
)
