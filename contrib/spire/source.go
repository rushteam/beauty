// Package spire 把 SPIFFE/SPIRE Workload API 接到 beauty:用 X509-SVID 做服务间 mTLS,
// 并把对端 SPIFFE ID 映射到 auth.User / authz.Subject。作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/spire)。
//
// 分层约定:
//   - 传输:本包 → tls.Config / gRPC TransportCredentials → grpcserver.WithTLSConfig /
//     WithGrpcServerOptions / grpcclient.WithGRPCDialOptions / webserver.WithTLSConfig /
//     resty.WithBaseTransport;
//   - 工作负载身份(可选):UnaryServerInterceptor / HTTPMiddleware 从 peer 证书取 SPIFFE ID;
//   - 终端用户身份:继续用 pkg/middleware/auth 的 JWT/token,与本包正交。
//
// 信任不来自服务发现 metadata;发现只给地址,本包只做密码学身份。
package spire

import (
	"context"
	"fmt"
	"sync"

	"github.com/rushteam/beauty/pkg/service/core"
	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Source 持有与 SPIRE Agent Workload API 的长连接,自动轮换 X509-SVID 与 trust bundle。
// 同时实现 x509svid.Source、x509bundle.Source 与 core.Component。
type Source struct {
	x509 *workloadapi.X509Source

	closeOnce sync.Once
	closeErr  error
}

var (
	_ x509svid.Source   = (*Source)(nil)
	_ x509bundle.Source = (*Source)(nil)
	_ core.Component    = (*Source)(nil)
)

// Option 配置 Connect。
type Option func(*connectConfig)

type connectConfig struct {
	addr string
}

// WithAddr 指定 Workload API 地址(如 unix:///tmp/spire-agent/public/api.sock)。
// 未设置时使用环境变量 SPIFFE_ENDPOINT_SOCKET。
func WithAddr(addr string) Option {
	return func(c *connectConfig) { c.addr = addr }
}

// Connect 连接 Workload API 并阻塞至收到首份 X509-SVID。
// 调用方须在用完后 Close;也可 beauty.WithComponent(source) 交给框架在停机时关闭。
func Connect(ctx context.Context, opts ...Option) (*Source, error) {
	cfg := &connectConfig{}
	for _, o := range opts {
		o(cfg)
	}
	var sourceOpts []workloadapi.X509SourceOption
	if cfg.addr != "" {
		sourceOpts = append(sourceOpts, workloadapi.WithClientOptions(workloadapi.WithAddr(cfg.addr)))
	}
	x509Src, err := workloadapi.NewX509Source(ctx, sourceOpts...)
	if err != nil {
		return nil, fmt.Errorf("spire: connect workload api: %w", err)
	}
	return &Source{x509: x509Src}, nil
}

// Name 实现 core.Component。
func (s *Source) Name() string { return "spire" }

// Init 实现 core.Component:返回的 CancelFunc 在应用停机时关闭 Workload API 连接。
// Connect 已完成建连,此处不再拨号。
func (s *Source) Init() context.CancelFunc {
	return func() { _ = s.Close() }
}

// Close 关闭 Workload API 连接。幂等。
func (s *Source) Close() error {
	s.closeOnce.Do(func() {
		if s.x509 != nil {
			s.closeErr = s.x509.Close()
		}
	})
	return s.closeErr
}

// GetX509SVID 实现 x509svid.Source。
func (s *Source) GetX509SVID() (*x509svid.SVID, error) {
	return s.x509.GetX509SVID()
}

// GetX509BundleForTrustDomain 实现 x509bundle.Source。
func (s *Source) GetX509BundleForTrustDomain(td spiffeid.TrustDomain) (*x509bundle.Bundle, error) {
	return s.x509.GetX509BundleForTrustDomain(td)
}

// X509Source 返回底层 *workloadapi.X509Source,供高级用法。
func (s *Source) X509Source() *workloadapi.X509Source { return s.x509 }

// SPIFFEID 返回当前工作负载的 SPIFFE ID。
func (s *Source) SPIFFEID() (spiffeid.ID, error) {
	svid, err := s.GetX509SVID()
	if err != nil {
		return spiffeid.ID{}, err
	}
	return svid.ID, nil
}
