package errors

import (
	"errors"
	"fmt"
)

// Status 是框架统一的业务错误类型。
// 实现标准 error 接口，可直接从 HTTP handler 或 gRPC handler 返回。
//
// HTTP recovery 中间件和 gRPC 拦截器会识别 *Status，
// 根据 Code 的注册映射自动转换为正确的 HTTP status code 或 gRPC status code。
// 普通 error 仍然兜底返回 500 / codes.Internal，向后兼容。
type Status struct {
	code    Code
	message string
	details []Detail
	cause   error // 原始错误，仅服务端日志，不序列化给客户端
}

// New 创建一个 Status。message 为空时使用 Code 的默认消息。
func New(code Code, message string) *Status {
	if message == "" {
		message = code.DefaultMessage()
	}
	return &Status{code: code, message: message}
}

// Newf 格式化 message 创建 Status。
func Newf(code Code, format string, args ...any) *Status {
	return New(code, fmt.Sprintf(format, args...))
}

// WithDetail 追加一个结构化详情，返回自身（链式调用）。
func (s *Status) WithDetail(d Detail) *Status {
	s.details = append(s.details, d)
	return s
}

// WithCause 记录原始错误（只用于服务端日志，不会序列化给客户端）。
func (s *Status) WithCause(err error) *Status {
	s.cause = err
	return s
}

// Code 返回业务错误码。
func (s *Status) Code() Code { return s.code }

// Message 返回面向用户的错误消息。
func (s *Status) Message() string { return s.message }

// Details 返回结构化详情列表。
func (s *Status) Details() []Detail { return s.details }

// Cause 返回原始错误（可能为 nil）。
func (s *Status) Cause() error { return s.cause }

// Error 实现 error 接口。
func (s *Status) Error() string {
	if s.cause != nil {
		return fmt.Sprintf("[%d] %s: %v", s.code, s.message, s.cause)
	}
	return fmt.Sprintf("[%d] %s", s.code, s.message)
}

// Unwrap 支持 errors.Is / errors.As 向上溯因。
func (s *Status) Unwrap() error { return s.cause }

// FromError 从任意 error 中提取 *Status。
// 若 err 本身或其链路中包含 *Status，返回 (status, true)；否则返回 (nil, false)。
func FromError(err error) (*Status, bool) {
	if err == nil {
		return nil, false
	}
	var s *Status
	if errors.As(err, &s) {
		return s, true
	}
	return nil, false
}

// Is 让 errors.Is 能够按 Code 匹配。
//
//	target := errors.New(ErrNotFound, "")
//	errors.Is(err, target)  // true 当 err 的 Code == ErrNotFound
func (s *Status) Is(target error) bool {
	t, ok := target.(*Status)
	if !ok {
		return false
	}
	return s.code == t.code
}
