package errors

// gRPC codes 常量（避免直接依赖 google.golang.org/grpc/codes 包）
const (
	grpcOK                 = 0
	grpcCanceled           = 1
	grpcUnknown            = 2
	grpcInvalidArgument    = 3
	grpcDeadlineExceeded   = 4
	grpcNotFound           = 5
	grpcAlreadyExists      = 6
	grpcPermissionDenied   = 7
	grpcResourceExhausted  = 8
	grpcFailedPrecondition = 9
	grpcAborted            = 10
	grpcOutOfRange         = 11
	grpcUnimplemented      = 12
	grpcInternal           = 13
	grpcUnavailable        = 14
	grpcDataLoss           = 15
	grpcUnauthenticated    = 16
)

// 框架预定义业务码（1–999 保留给框架）
const (
	CodeOK Code = 0

	// 4xx 客户端错误
	CodeInvalidArgument    Code = 400 // 参数非法
	CodeUnauthenticated    Code = 401 // 未认证
	CodeForbidden          Code = 403 // 无权限
	CodeNotFound           Code = 404 // 资源不存在
	CodeConflict           Code = 409 // 资源冲突/已存在
	CodeTooManyRequests    Code = 429 // 请求过频
	CodeFailedPrecondition Code = 412 // 前置条件不满足

	// 5xx 服务端错误
	CodeInternal      Code = 500 // 内部错误
	CodeUnimplemented Code = 501 // 未实现
	CodeUnavailable   Code = 503 // 服务不可用
	CodeDeadline      Code = 504 // 超时
)

func init() {
	Register(CodeInvalidArgument, 400, grpcInvalidArgument, "invalid argument")
	Register(CodeUnauthenticated, 401, grpcUnauthenticated, "unauthenticated")
	Register(CodeForbidden, 403, grpcPermissionDenied, "forbidden")
	Register(CodeNotFound, 404, grpcNotFound, "not found")
	Register(CodeConflict, 409, grpcAlreadyExists, "conflict")
	Register(CodeTooManyRequests, 429, grpcResourceExhausted, "too many requests")
	Register(CodeFailedPrecondition, 412, grpcFailedPrecondition, "failed precondition")
	Register(CodeInternal, 500, grpcInternal, "internal server error")
	Register(CodeUnimplemented, 501, grpcUnimplemented, "not implemented")
	Register(CodeUnavailable, 503, grpcUnavailable, "service unavailable")
	Register(CodeDeadline, 504, grpcDeadlineExceeded, "deadline exceeded")
}

// 快捷构造函数，对应每个预定义码

func InvalidArgument(msg string) *Status { return New(CodeInvalidArgument, msg) }
func Unauthenticated(msg string) *Status { return New(CodeUnauthenticated, msg) }
func Forbidden(msg string) *Status       { return New(CodeForbidden, msg) }
func NotFound(msg string) *Status        { return New(CodeNotFound, msg) }
func Conflict(msg string) *Status        { return New(CodeConflict, msg) }
func TooManyRequests(msg string) *Status { return New(CodeTooManyRequests, msg) }
func Internal(msg string) *Status        { return New(CodeInternal, msg) }
func Unimplemented(msg string) *Status   { return New(CodeUnimplemented, msg) }
func Unavailable(msg string) *Status     { return New(CodeUnavailable, msg) }
