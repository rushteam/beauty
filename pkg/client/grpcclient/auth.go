package grpcclient

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// BearerToken 实现 grpc/credentials.PerRPCCredentials，
// 在每次 RPC 自动注入 "Authorization: Bearer <token>"。
type BearerToken string

func (t BearerToken) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(t)}, nil
}

func (t BearerToken) RequireTransportSecurity() bool { return false }

var _ credentials.PerRPCCredentials = BearerToken("")

// DialWithBearer 是注入 Bearer Token 的便捷拨号入口。
// target 含 scheme（k8s://、etcd:// 等）时走服务发现；
// 否则视为 host:port 直连。默认注入 Insecure 传输（内部服务间通信），
// 需要 TLS 时通过 opts 传入 WithGRPCDialOptions(grpc.WithTransportCredentials(...))
// 并覆盖默认的 Insecure。
func DialWithBearer(target, token string, opts ...DialOption) (*grpc.ClientConn, error) {
	bearer := WithGRPCDialOptions(grpc.WithPerRPCCredentials(BearerToken(token)))
	var defaults []DialOption
	if !hasScheme(target) {
		defaults = []DialOption{WithDirect(), WithInsecure(), bearer}
	} else {
		defaults = []DialOption{WithInsecure(), bearer}
	}
	// 用户 opts 在后，可覆盖默认的 Insecure
	return Dial(target, append(defaults, opts...)...)
}

func hasScheme(target string) bool {
	i := strings.Index(target, "://")
	return i > 0 && !strings.ContainsAny(target[:i], " /")
}
