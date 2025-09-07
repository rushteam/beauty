package grpcclient

import (
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
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

	var filtered []discover.ServiceInfo
	for _, service := range services {
		if f.Matches(service.Metadata) {
			filtered = append(filtered, service)
		}
	}

	// 如果没有匹配的实例，返回所有实例（容错机制）
	if len(filtered) == 0 {
		logger.Warn("no services match label selector, using all available services",
			"selector", f.String())
		return services
	}

	return filtered
}

// NewLabelFilter 为了向后兼容，保留原来的函数名
func NewLabelFilter() *ServiceLabelFilter {
	return NewServiceLabelFilter()
}
