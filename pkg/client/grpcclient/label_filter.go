package grpcclient

import (
	"log/slog"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

// ServiceLabelFilter gRPC服务标签过滤器，基于通用的LabelFilter
type ServiceLabelFilter struct {
	*selector.LabelFilter
}

// NewServiceLabelFilter 创建服务标签过滤器
func NewServiceLabelFilter() *ServiceLabelFilter {
	return &ServiceLabelFilter{
		LabelFilter: selector.NewLabelFilter(),
	}
}

// WithRegionIn 添加地域 in 表达式（便捷方法）
func (f *ServiceLabelFilter) WithRegionIn(regions ...string) *ServiceLabelFilter {
	f.LabelFilter.WithRegionIn(regions...)
	return f
}

// WithZoneIn 添加可用区 in 表达式（便捷方法）
func (f *ServiceLabelFilter) WithZoneIn(zones ...string) *ServiceLabelFilter {
	f.LabelFilter.WithZoneIn(zones...)
	return f
}

// WithCampusIn 添加园区 in 表达式（便捷方法）
func (f *ServiceLabelFilter) WithCampusIn(campuses ...string) *ServiceLabelFilter {
	f.LabelFilter.WithCampusIn(campuses...)
	return f
}

// WithEnvironmentIn 添加环境 in 表达式（便捷方法）
func (f *ServiceLabelFilter) WithEnvironmentIn(environments ...string) *ServiceLabelFilter {
	f.LabelFilter.WithEnvironmentIn(environments...)
	return f
}

// WithVersionIn 只路由到 version 标签在给定集合中的实例，用于灰度发布。
// 服务端通过 grpcserver.WithVersion("v2") / webserver.WithVersion("v2") 写入 metadata，
// 客户端通过 WithVersionIn("v2") 过滤，实现版本级流量隔离。
func (f *ServiceLabelFilter) WithVersionIn(versions ...string) *ServiceLabelFilter {
	f.LabelFilter.WithExpression("version", selector.FilterOpIn, versions...)
	return f
}

// WithMatchLabel 添加单个精确匹配的标签
func (f *ServiceLabelFilter) WithMatchLabel(key, value string) *ServiceLabelFilter {
	f.LabelFilter.WithMatchLabel(key, value)
	return f
}

// WithMatchLabels 添加精确匹配的标签
func (f *ServiceLabelFilter) WithMatchLabels(labels map[string]string) *ServiceLabelFilter {
	f.LabelFilter.WithMatchLabels(labels)
	return f
}

// WithExpression 添加表达式匹配
func (f *ServiceLabelFilter) WithExpression(key string, operator selector.FilterOperator, values ...string) *ServiceLabelFilter {
	f.LabelFilter.WithExpression(key, operator, values...)
	return f
}

// Filter 过滤服务实例
func (f *ServiceLabelFilter) Filter(services []discover.ServiceInfo) []discover.ServiceInfo {
	if f.LabelFilter == nil {
		return services
	}

	filtered := make([]discover.ServiceInfo, 0, len(services))
	for _, service := range services {
		if f.Matches(service.Metadata) {
			filtered = append(filtered, service)
		}
	}

	// fail-closed：没有匹配的实例时返回空，而不是退回全量。
	// 退回全量会静默击穿 region/zone/version 等隔离约束——例如把请求路由到
	// 错误的地域或不兼容的版本，比"无可用实例"更危险。调用方应据空结果报错。
	if len(filtered) == 0 {
		slog.Warn("no services match label selector; returning empty (fail-closed)",
			"selector", f.String(), "total", len(services))
	}

	return filtered
}

// NewLabelFilter 为了向后兼容，保留原来的函数名
func NewLabelFilter() *ServiceLabelFilter {
	return NewServiceLabelFilter()
}
