package grpcclient

import (
	"fmt"
	"strings"
)

// RetryPolicy 对应 gRPC ServiceConfig 中的 retryPolicy 字段。
// 注入到 grpc.WithDefaultServiceConfig 后，由 gRPC 传输层自动处理，业务代码无感知。
//
// 适用场景：
//   - 服务端滚动发布期间短暂返回 UNAVAILABLE
//   - 网络抖动导致的瞬态连接失败
//   - 服务端触发背压返回 RESOURCE_EXHAUSTED
//
// 与 Call()/failover 的区别：
//   - RetryPolicy 在单个 conn.Invoke() 内部由 gRPC 自动重试，不换节点
//   - Call()/failover 每次重试重新 GetClient() 选节点，处理节点彻底不可用的情况
//   - 两者互补，建议同时使用
type RetryPolicy struct {
	// MaxAttempts 最大尝试次数（含首次），范围 [2, 5]，gRPC 规范限制上限为 5。
	MaxAttempts int
	// InitialBackoff 首次重试等待时间，格式为 Go duration string，如 "0.1s"。
	InitialBackoff string
	// MaxBackoff 最大退避时间。
	MaxBackoff string
	// BackoffMultiplier 退避倍数，每次重试等待时间乘以该值。
	BackoffMultiplier float64
	// RetryableStatusCodes 触发重试的 gRPC 状态码。
	// 常用：UNAVAILABLE（服务不可用）、RESOURCE_EXHAUSTED（过载）。
	// 注意：DEADLINE_EXCEEDED 和 CANCELLED 不能加入此列表（gRPC 规范禁止）。
	RetryableStatusCodes []string
}

// DefaultRetryPolicy 返回适合大多数微服务场景的默认 retry policy：
//   - 最多 3 次尝试（1 次首次 + 2 次重试）
//   - 首次重试等 100ms，最长等 1s，指数退避 2 倍
//   - 仅对 UNAVAILABLE 重试（最保守，幂等安全）
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:          3,
		InitialBackoff:       "0.1s",
		MaxBackoff:           "1s",
		BackoffMultiplier:    2.0,
		RetryableStatusCodes: []string{"UNAVAILABLE"},
	}
}

// WithResourceExhausted 在默认策略基础上追加 RESOURCE_EXHAUSTED 重试，
// 适用于服务端限流会短暂返回该状态码的场景。
func (p RetryPolicy) WithResourceExhausted() RetryPolicy {
	p.RetryableStatusCodes = append(p.RetryableStatusCodes, "RESOURCE_EXHAUSTED")
	return p
}

// serviceConfig 将 RetryPolicy 序列化为 gRPC ServiceConfig JSON 字符串。
// name 为空对象 {} 表示匹配所有服务的所有方法。
// 直接用 fmt.Sprintf 拼模板，不依赖 encoding/json，不会出错。
func (p RetryPolicy) serviceConfig() string {
	maxAttempts := p.MaxAttempts
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	if maxAttempts > 5 {
		maxAttempts = 5
	}

	// 状态码列表：["UNAVAILABLE","RESOURCE_EXHAUSTED"]
	quoted := make([]string, len(p.RetryableStatusCodes))
	for i, c := range p.RetryableStatusCodes {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	codes := "[" + strings.Join(quoted, ",") + "]"

	return fmt.Sprintf(`{"methodConfig":[{"name":[{}],"retryPolicy":{`+
		`"maxAttempts":%d,`+
		`"initialBackoff":"%s",`+
		`"maxBackoff":"%s",`+
		`"backoffMultiplier":%.1f,`+
		`"retryableStatusCodes":%s`+
		`}}]}`,
		maxAttempts,
		p.InitialBackoff,
		p.MaxBackoff,
		p.BackoffMultiplier,
		codes,
	)
}
