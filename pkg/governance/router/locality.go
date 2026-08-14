package router

import "github.com/rushteam/beauty/pkg/service/discover"

// 默认 fallback 层级顺序（从最近到最远）。
var defaultTiers = []string{"campus", "zone", "region"}

// Locality 描述调用方所在的地域信息，与服务注册时 metadata 中的同名 key 对应。
//
//	grpcserver.WithRegionInfo(region, zone, campus) // 服务端注册
//	router.Locality{Region: "cn-east", Zone: "cn-east-1a"} // 客户端声明
type Locality struct {
	Region string // 如 "cn-east"、"us-west-1"
	Zone   string // 如 "cn-east-1a"、"us-west-1b"
	Campus string // 如 "campus-1"（可选，数据中心内园区）
}

// tierValue 返回该 Locality 在指定 tier key 上的值。
func (l Locality) tierValue(key string) string {
	switch key {
	case "region":
		return l.Region
	case "zone":
		return l.Zone
	case "campus":
		return l.Campus
	default:
		return ""
	}
}

// LocalityOption 配置 LocalityRouter。
type LocalityOption func(*LocalityRouter)

// WithGlobalFallback 设置所有层级都无匹配时是否退回全量实例。
// 默认 false（fail-closed：无匹配时返回空，调用方报错）。
// 设为 true 后，即使没有同区域的实例也能路由（牺牲延迟换可用性）。
func WithGlobalFallback(enable bool) LocalityOption {
	return func(r *LocalityRouter) { r.globalFallback = enable }
}

// WithTiers 自定义 fallback 层级顺序和使用的 metadata key。
// 默认 ["campus", "zone", "region"]（从最近到最远）。
//
// 可用于跳过某些层级或引入自定义维度：
//
//	WithTiers("rack", "zone", "region") // 加入机架级别
//	WithTiers("zone", "region")         // 跳过 campus
func WithTiers(keys ...string) LocalityOption {
	return func(r *LocalityRouter) { r.tiers = keys }
}

// LocalityRouter 实现地域亲和路由：按层级（默认 campus → zone → region）逐级
// 尝试匹配，返回最近一级的匹配结果。
//
// 与 LabelRouter 的区别：LabelRouter 是硬匹配（匹配或 fail-closed），
// LocalityRouter 是分级 fallback（优先就近，逐级放宽）。
//
// 典型用法：
//
//	r := router.NewLocalityRouter(router.Locality{
//	    Region: "cn-east",
//	    Zone:   "cn-east-1a",
//	}, router.WithGlobalFallback(true))
//
//	client := grpcclient.NewServiceDiscoveryClient(reg, "payment",
//	    grpcclient.WithServiceRouter(r),
//	)
//
// 无状态，并发安全。
type LocalityRouter struct {
	local          Locality
	tiers          []string
	globalFallback bool
}

// NewLocalityRouter 创建地域亲和路由器。
// local 声明调用方自身的地域信息，用于与服务实例 metadata 中的对应字段匹配。
func NewLocalityRouter(local Locality, opts ...LocalityOption) *LocalityRouter {
	r := &LocalityRouter{
		local: local,
		tiers: defaultTiers,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Filter 按层级逐级匹配：遍历 tiers，对每一级用调用方的本地值过滤实例，
// 第一个非空结果即返回。所有层级都无匹配时，根据 globalFallback 决定返回全量或空。
func (r *LocalityRouter) Filter(_ string, nodes []discover.ServiceInfo) []discover.ServiceInfo {
	if len(nodes) == 0 {
		return nodes
	}
	for _, key := range r.tiers {
		val := r.local.tierValue(key)
		if val == "" {
			continue
		}
		matched := filterByMetadata(nodes, key, val)
		if len(matched) > 0 {
			return matched
		}
	}
	if r.globalFallback {
		return nodes
	}
	return nil
}

func filterByMetadata(nodes []discover.ServiceInfo, key, value string) []discover.ServiceInfo {
	out := make([]discover.ServiceInfo, 0, len(nodes))
	for _, n := range nodes {
		if n.Metadata[key] == value {
			out = append(out, n)
		}
	}
	return out
}

var _ ServiceRouter = (*LocalityRouter)(nil)
