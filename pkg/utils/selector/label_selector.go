package selector

import (
	"fmt"
	"strings"
)

// FilterOperator 定义过滤操作符，参考 Kubernetes Label Selector
type FilterOperator string

const (
	// 基于等式的操作符
	FilterOpEquals    FilterOperator = "="  // 等于
	FilterOpNotEquals FilterOperator = "!=" // 不等于

	// 基于集合的操作符
	FilterOpIn       FilterOperator = "in"       // 在集合中
	FilterOpNotIn    FilterOperator = "notin"    // 不在集合中
	FilterOpExists   FilterOperator = "exists"   // 标签存在
	FilterOpNotExist FilterOperator = "notexist" // 标签不存在
)

// LabelSelectorRequirement 表示单个标签选择器需求
type LabelSelectorRequirement struct {
	Key      string         `json:"key"`      // 标签键
	Operator FilterOperator `json:"operator"` // 操作符
	Values   []string       `json:"values"`   // 值列表
}

// LabelSelector 标签选择器，参考 Kubernetes 设计
type LabelSelector struct {
	// MatchLabels 是一个键值对映射，所有键值对都必须匹配
	MatchLabels map[string]string `json:"matchLabels,omitempty"`

	// MatchExpressions 是标签选择器需求的列表
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// LabelFilter 通用的标签过滤器，基于 Kubernetes LabelSelector 设计
type LabelFilter struct {
	selector *LabelSelector
}

// NewLabelFilter 创建新的标签过滤器
func NewLabelFilter() *LabelFilter {
	return &LabelFilter{
		selector: &LabelSelector{
			MatchLabels:      make(map[string]string),
			MatchExpressions: make([]LabelSelectorRequirement, 0),
		},
	}
}

// WithMatchLabels 添加精确匹配的标签
func (f *LabelFilter) WithMatchLabels(labels map[string]string) *LabelFilter {
	if f.selector.MatchLabels == nil {
		f.selector.MatchLabels = make(map[string]string)
	}
	for k, v := range labels {
		f.selector.MatchLabels[k] = v
	}
	return f
}

// WithMatchLabel 添加单个精确匹配的标签
func (f *LabelFilter) WithMatchLabel(key, value string) *LabelFilter {
	if f.selector.MatchLabels == nil {
		f.selector.MatchLabels = make(map[string]string)
	}
	f.selector.MatchLabels[key] = value
	return f
}

// WithExpression 添加表达式匹配
func (f *LabelFilter) WithExpression(key string, operator FilterOperator, values ...string) *LabelFilter {
	requirement := LabelSelectorRequirement{
		Key:      key,
		Operator: operator,
		Values:   values,
	}
	f.selector.MatchExpressions = append(f.selector.MatchExpressions, requirement)
	return f
}

// WithRegionIn 添加地域 in 表达式（便捷方法）
func (f *LabelFilter) WithRegionIn(regions ...string) *LabelFilter {
	if len(regions) > 0 {
		return f.WithExpression("region", FilterOpIn, regions...)
	}
	return f
}

// WithZoneIn 添加可用区 in 表达式（便捷方法）
func (f *LabelFilter) WithZoneIn(zones ...string) *LabelFilter {
	if len(zones) > 0 {
		return f.WithExpression("zone", FilterOpIn, zones...)
	}
	return f
}

// WithCampusIn 添加园区 in 表达式（便捷方法）
func (f *LabelFilter) WithCampusIn(campuses ...string) *LabelFilter {
	if len(campuses) > 0 {
		return f.WithExpression("campus", FilterOpIn, campuses...)
	}
	return f
}

// WithEnvironmentIn 添加环境 in 表达式（便捷方法）
func (f *LabelFilter) WithEnvironmentIn(environments ...string) *LabelFilter {
	if len(environments) > 0 {
		return f.WithExpression("environment", FilterOpIn, environments...)
	}
	return f
}

// Matches 检查给定的标签是否匹配选择器
func (f *LabelFilter) Matches(labels map[string]string) bool {
	if f.selector == nil || (len(f.selector.MatchLabels) == 0 && len(f.selector.MatchExpressions) == 0) {
		return true
	}
	return f.matches(labels)
}

// FilterMap 过滤带有标签的对象切片，使用自定义的标签提取函数
func (f *LabelFilter) FilterMap(items interface{}, getLabelsFn func(interface{}) map[string]string) []interface{} {
	if f.selector == nil || (len(f.selector.MatchLabels) == 0 && len(f.selector.MatchExpressions) == 0) {
		// 如果没有过滤条件，返回原始切片
		if slice, ok := items.([]interface{}); ok {
			return slice
		}
		return nil
	}

	var filtered []interface{}

	// 使用反射处理不同类型的切片
	switch v := items.(type) {
	case []interface{}:
		for _, item := range v {
			if f.matches(getLabelsFn(item)) {
				filtered = append(filtered, item)
			}
		}
	default:
		// 对于其他类型，返回空切片
		return []interface{}{}
	}

	return filtered
}

// matches 检查服务元数据是否匹配选择器
func (f *LabelFilter) matches(metadata map[string]string) bool {
	// 检查 MatchLabels
	for key, value := range f.selector.MatchLabels {
		if metadata[key] != value {
			return false
		}
	}

	// 检查 MatchExpressions
	for _, requirement := range f.selector.MatchExpressions {
		if !f.matchesRequirement(metadata, requirement) {
			return false
		}
	}

	return true
}

// matchesRequirement 检查单个需求是否匹配
func (f *LabelFilter) matchesRequirement(metadata map[string]string, req LabelSelectorRequirement) bool {
	value, exists := metadata[req.Key]

	switch req.Operator {
	case FilterOpEquals:
		if len(req.Values) != 1 {
			return false
		}
		return exists && value == req.Values[0]

	case FilterOpNotEquals:
		if len(req.Values) != 1 {
			return false
		}
		return !exists || value != req.Values[0]

	case FilterOpIn:
		if !exists {
			return false
		}
		return contains(req.Values, value)

	case FilterOpNotIn:
		if !exists {
			return true // 不存在的标签视为不在集合中
		}
		return !contains(req.Values, value)

	case FilterOpExists:
		return exists

	case FilterOpNotExist:
		return !exists

	default:
		// 未知操作符，返回 false
		return false
	}
}

// String 返回选择器的字符串表示
func (f *LabelFilter) String() string {
	if f.selector == nil {
		return "{}"
	}

	var parts []string

	// MatchLabels
	for k, v := range f.selector.MatchLabels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}

	// MatchExpressions
	for _, req := range f.selector.MatchExpressions {
		switch req.Operator {
		case FilterOpEquals:
			if len(req.Values) == 1 {
				parts = append(parts, fmt.Sprintf("%s=%s", req.Key, req.Values[0]))
			}
		case FilterOpNotEquals:
			if len(req.Values) == 1 {
				parts = append(parts, fmt.Sprintf("%s!=%s", req.Key, req.Values[0]))
			}
		case FilterOpIn:
			parts = append(parts, fmt.Sprintf("%s in (%s)", req.Key, strings.Join(req.Values, ",")))
		case FilterOpNotIn:
			parts = append(parts, fmt.Sprintf("%s notin (%s)", req.Key, strings.Join(req.Values, ",")))
		case FilterOpExists:
			parts = append(parts, req.Key)
		case FilterOpNotExist:
			parts = append(parts, fmt.Sprintf("!%s", req.Key))
		}
	}

	return strings.Join(parts, ",")
}

// contains 辅助函数，检查切片是否包含指定值
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
