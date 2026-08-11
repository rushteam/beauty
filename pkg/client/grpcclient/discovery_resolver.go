package grpcclient

import (
	"github.com/rushteam/beauty/pkg/service/discover"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

const discoveryScheme = "beauty-discovery"

// discoveryBuilder 是 per-connection 的 resolver.Builder，
// 把 caller 提供的 registry 接入 gRPC 原生 resolver 机制。
// 通过 grpc.WithResolvers(b) 注入，不污染全局 resolver 注册表。
type discoveryBuilder struct {
	registry    discover.Discovery
	serviceName string
	labelFilter *ServiceLabelFilter
}

var _ resolver.Builder = (*discoveryBuilder)(nil)

func (b *discoveryBuilder) Scheme() string { return discoveryScheme }

func (b *discoveryBuilder) Build(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	var wrapper resolver.ClientConn = cc
	if b.labelFilter != nil {
		wrapper = &filteringClientConn{cc: cc, filter: b.labelFilter}
	}
	r := NewResolver(wrapper, b.serviceName, b.registry)
	go r.Start()
	return r, nil
}

// WrapWithFilter 根据 URL 查询参数构建标签过滤器并包装 resolver.ClientConn。
// 如果 URL 中不含过滤参数，直接返回原始 cc。供各 resolver builder 统一使用。
func WrapWithFilter(cc resolver.ClientConn, query map[string][]string) resolver.ClientConn {
	params := make(map[string]string, len(query))
	for k, v := range query {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	filter := buildFilterFromParams(params)
	if filter == nil {
		return cc
	}
	return &filteringClientConn{cc: cc, filter: filter}
}

// filteringClientConn 在 UpdateState 前对地址列表应用标签过滤。
// 包装 resolver.ClientConn 而非修改 Resolver，保持 Resolver 通用不变。
type filteringClientConn struct {
	cc     resolver.ClientConn
	filter *ServiceLabelFilter
}

func (f *filteringClientConn) UpdateState(state resolver.State) error {
	services := addressesToServiceInfos(state.Addresses)
	filtered := f.filter.Filter(services)
	state.Addresses = serviceInfosToAddresses(filtered)
	return f.cc.UpdateState(state)
}

func (f *filteringClientConn) ReportError(err error) { f.cc.ReportError(err) }

func (f *filteringClientConn) NewAddress(addrs []resolver.Address) {
	f.cc.NewAddress(addrs) //nolint:staticcheck
}

func (f *filteringClientConn) ParseServiceConfig(configJSON string) *serviceconfig.ParseResult {
	return f.cc.ParseServiceConfig(configJSON)
}

// addressesToServiceInfos 从 resolver.Address 切片反构建 ServiceInfo（用于标签过滤）。
// Metadata 存储在 Attributes 中，buildState 写入时用 string key→string value。
func addressesToServiceInfos(addrs []resolver.Address) []discover.ServiceInfo {
	infos := make([]discover.ServiceInfo, len(addrs))
	for i, a := range addrs {
		infos[i] = discover.ServiceInfo{
			Addr:     a.Addr,
			Metadata: extractMetadata(a),
		}
	}
	return infos
}

func serviceInfosToAddresses(services []discover.ServiceInfo) []resolver.Address {
	state := buildState(services)
	return state.Addresses
}

// extractMetadata 从 resolver.Address.Attributes 提取 metadata map。
// buildState 在写入时把每个 metadata kv 作为 Attributes.WithValue(k, v) 存储，
// 但 grpc/attributes 不支持遍历——所以这里用 ServerName 做 fallback（不完美但实用）。
// 如果需要完整的 metadata 传递，可通过 Attributes 存整个 map。
func extractMetadata(a resolver.Address) map[string]string {
	if a.Attributes == nil {
		return nil
	}
	if m, ok := a.Attributes.Value(metadataAttrKey{}).(map[string]string); ok {
		return m
	}
	return nil
}
