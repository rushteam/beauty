package errors

import "time"

// Detail 是结构化错误详情的接口。
// 所有内置详情类型和业务自定义详情类型都实现它。
type Detail interface {
	detailType() string
}

// FieldViolation 字段校验失败，适用于参数校验错误（400）。
// 一个 Status 可以携带多个 FieldViolation，每个对应一个非法字段。
type FieldViolation struct {
	Field       string // 字段名，如 "user.email"
	Description string // 描述，如 "must be a valid email address"
}

func (f *FieldViolation) detailType() string { return "FieldViolation" }

// RetryInfo 告知客户端建议的重试等待时间，适用于限流（429）或服务暂时不可用（503）。
type RetryInfo struct {
	RetryDelay time.Duration
}

func (r *RetryInfo) detailType() string { return "RetryInfo" }

// ResourceInfo 描述操作涉及的资源，适用于 NotFound（404）或 AlreadyExists（409）。
type ResourceInfo struct {
	ResourceType string // 资源类型，如 "User"、"Order"
	Name         string // 资源标识，如 id 或 name
	Description  string // 可选的补充说明
}

func (r *ResourceInfo) detailType() string { return "ResourceInfo" }

// QuotaViolation 配额超限，适用于 429 场景，描述哪个配额被超限。
type QuotaViolation struct {
	Subject     string // 配额主体，如 "project/my-project/quota/read-requests-per-day"
	Description string // 如 "daily read quota exceeded"
}

func (q *QuotaViolation) detailType() string { return "QuotaViolation" }

// ErrorInfo 携带错误的机器可读原因和元数据，适合在微服务间传递结构化错误原因。
type ErrorInfo struct {
	Reason   string            // 错误原因标识，如 "USER_NOT_FOUND"，建议全大写下划线
	Domain   string            // 服务域，如 "user-service"
	Metadata map[string]string // 扩展 KV，如 {"user_id": "123"}
}

func (e *ErrorInfo) detailType() string { return "ErrorInfo" }
