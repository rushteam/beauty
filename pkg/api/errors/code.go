package errors

import "net/http"

// Code 业务错误码，32 位整数。
// 框架预留 1-999，业务自定义从 1000 起。
// 推荐约定：高位表示模块，低位表示具体错误，例如 10404 = 通用模块-不存在。
type Code int32

// codeMeta 记录一个 Code 的 HTTP status 和 gRPC code 映射。
type codeMeta struct {
	httpStatus int
	grpcCode   uint32 // google.golang.org/grpc/codes.Code 底层是 uint32
	defaultMsg string
}

var registry = map[Code]codeMeta{}

// Register 注册一个业务码的映射关系。
// 框架内置码在 predefined.go 中通过 init() 注册；业务方在自己的 init() 中调用。
// 重复注册同一个 Code 会 panic，防止无声覆盖。
func Register(code Code, httpStatus int, grpcCode uint32, defaultMsg string) {
	if _, dup := registry[code]; dup {
		panic("errors: duplicate code registration: " + itoa(int(code)))
	}
	registry[code] = codeMeta{
		httpStatus: httpStatus,
		grpcCode:   grpcCode,
		defaultMsg: defaultMsg,
	}
}

// HTTPStatus 返回该 Code 对应的 HTTP status code；未注册时返回 500。
func (c Code) HTTPStatus() int {
	if m, ok := registry[c]; ok {
		return m.httpStatus
	}
	return http.StatusInternalServerError
}

// GRPCCode 返回该 Code 对应的 gRPC code 值；未注册时返回 13（codes.Internal）。
func (c Code) GRPCCode() uint32 {
	if m, ok := registry[c]; ok {
		return m.grpcCode
	}
	return 13 // codes.Internal
}

// DefaultMessage 返回该 Code 的默认错误信息。
func (c Code) DefaultMessage() string {
	if m, ok := registry[c]; ok {
		return m.defaultMsg
	}
	return "internal server error"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
