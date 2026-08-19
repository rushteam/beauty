package spire

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"

	"github.com/rushteam/beauty/pkg/api/authz"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// ErrNoPeerID 表示请求上下文中没有可用的 SPIFFE ID(非 mTLS 或凭证未暴露 ID)。
var ErrNoPeerID = errors.New("spire: no peer spiffe id")

// PeerIDFromContext 从 gRPC context 提取对端 SPIFFE ID。
// 优先走 go-spiffe grpccredentials 包装;回退到标准 credentials.TLSInfo.SPIFFEID。
func PeerIDFromContext(ctx context.Context) (spiffeid.ID, error) {
	if id, ok := grpccredentials.PeerIDFromContext(ctx); ok {
		return id, nil
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return spiffeid.ID{}, ErrNoPeerID
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || tlsInfo.SPIFFEID == nil {
		return spiffeid.ID{}, ErrNoPeerID
	}
	id, err := spiffeid.FromString(tlsInfo.SPIFFEID.String())
	if err != nil {
		return spiffeid.ID{}, err
	}
	return id, nil
}

// PeerIDFromRequest 从 HTTP 请求的 TLS 对端证书提取 SPIFFE ID。
func PeerIDFromRequest(r *http.Request) (spiffeid.ID, error) {
	if r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return spiffeid.ID{}, ErrNoPeerID
	}
	return idFromCerts(r.TLS.PeerCertificates)
}

func idFromCerts(certs []*x509.Certificate) (spiffeid.ID, error) {
	if len(certs) == 0 {
		return spiffeid.ID{}, ErrNoPeerID
	}
	id, err := x509svid.IDFromCert(certs[0])
	if err != nil {
		return spiffeid.ID{}, err
	}
	return id, nil
}

// UserFromID 把 SPIFFE ID 映射为 auth.User。ID/Name 均为完整 SPIFFE ID 字符串。
func UserFromID(id spiffeid.ID, roles ...string) auth.User {
	s := id.String()
	return auth.NewUser(s, s, roles)
}

// SubjectFromID 把 SPIFFE ID 映射为 authz.Subject。
func SubjectFromID(id spiffeid.ID, roles ...string) authz.Subject {
	return authz.Subject{
		ID:    id.String(),
		Roles: roles,
		Attrs: map[string]string{
			"spiffe_id":    id.String(),
			"trust_domain": id.TrustDomain().String(),
			"path":         id.Path(),
		},
	}
}

// AuthOption 配置 SPIFFE 身份中间件。
type AuthOption func(*authConfig)

type authConfig struct {
	roles     []string
	skipPaths []string
	withAuthz bool
	onSuccess func(ctx context.Context, id spiffeid.ID)
	onFailure func(ctx context.Context, err error)
}

// WithRoles 给映射出的 User/Subject 附加静态角色(供后续 authz 使用)。
func WithRoles(roles ...string) AuthOption {
	return func(c *authConfig) { c.roles = append([]string(nil), roles...) }
}

// WithSkipPaths 跳过认证的 gRPC 方法全名或 HTTP 路径。
func WithSkipPaths(paths ...string) AuthOption {
	return func(c *authConfig) { c.skipPaths = append([]string(nil), paths...) }
}

// WithAuthzSubject 认证成功后把 Subject 写入 context(authz.ContextWithSubject)。
func WithAuthzSubject() AuthOption {
	return func(c *authConfig) { c.withAuthz = true }
}

// WithOnAuthSuccess 认证成功回调(须轻量、不阻塞)。
func WithOnAuthSuccess(fn func(ctx context.Context, id spiffeid.ID)) AuthOption {
	return func(c *authConfig) { c.onSuccess = fn }
}

// WithOnAuthFailure 认证失败回调(须轻量、不阻塞)。
func WithOnAuthFailure(fn func(ctx context.Context, err error)) AuthOption {
	return func(c *authConfig) { c.onFailure = fn }
}

func newAuthConfig(opts []AuthOption) *authConfig {
	c := &authConfig{}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *authConfig) shouldSkip(path string) bool {
	for _, p := range c.skipPaths {
		if p == path {
			return true
		}
	}
	return false
}

// UnaryServerInterceptor 从 mTLS peer 取 SPIFFE ID,写入 auth.User(及可选 authz.Subject)。
// 须配合 MTLSServerCredentials 使用(或能暴露 TLSInfo.SPIFFEID 的凭证)。
func UnaryServerInterceptor(opts ...AuthOption) grpc.UnaryServerInterceptor {
	cfg := newAuthConfig(opts)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if cfg.shouldSkip(info.FullMethod) {
			return handler(ctx, req)
		}
		id, err := PeerIDFromContext(ctx)
		if err != nil {
			if cfg.onFailure != nil {
				cfg.onFailure(ctx, err)
			}
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}
		user := UserFromID(id, cfg.roles...)
		ctx = auth.WithUser(ctx, user)
		if cfg.withAuthz {
			ctx = authz.ContextWithSubject(ctx, SubjectFromID(id, cfg.roles...))
		}
		if cfg.onSuccess != nil {
			cfg.onSuccess(ctx, id)
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor 同 UnaryServerInterceptor,用于流式 RPC。
func StreamServerInterceptor(opts ...AuthOption) grpc.StreamServerInterceptor {
	cfg := newAuthConfig(opts)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if cfg.shouldSkip(info.FullMethod) {
			return handler(srv, ss)
		}
		id, err := PeerIDFromContext(ss.Context())
		if err != nil {
			if cfg.onFailure != nil {
				cfg.onFailure(ss.Context(), err)
			}
			return status.Error(codes.Unauthenticated, err.Error())
		}
		ctx := auth.WithUser(ss.Context(), UserFromID(id, cfg.roles...))
		if cfg.withAuthz {
			ctx = authz.ContextWithSubject(ctx, SubjectFromID(id, cfg.roles...))
		}
		if cfg.onSuccess != nil {
			cfg.onSuccess(ctx, id)
		}
		return handler(srv, &ctxServerStream{ServerStream: ss, ctx: ctx})
	}
}

type ctxServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ctxServerStream) Context() context.Context { return s.ctx }

// HTTPMiddleware 从 TLS 对端证书取 SPIFFE ID,写入 auth.User(及可选 authz.Subject)。
// 服务端须启用 mTLS(如 webserver.WithTLSConfig(source.MTLSServerConfig(...)))。
func HTTPMiddleware(opts ...AuthOption) func(http.Handler) http.Handler {
	cfg := newAuthConfig(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.shouldSkip(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			id, err := PeerIDFromRequest(r)
			if err != nil {
				if cfg.onFailure != nil {
					cfg.onFailure(r.Context(), err)
				}
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			ctx := auth.WithUser(r.Context(), UserFromID(id, cfg.roles...))
			if cfg.withAuthz {
				ctx = authz.ContextWithSubject(ctx, SubjectFromID(id, cfg.roles...))
			}
			if cfg.onSuccess != nil {
				cfg.onSuccess(ctx, id)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
