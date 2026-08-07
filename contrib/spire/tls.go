package spire

import (
	"crypto/tls"
	"net/http"

	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
)

// Authorizer 是对端 SPIFFE ID 授权函数。可直接使用本包导出的 Authorize* 助手,
// 或传入 tlsconfig.Authorizer / 自定义函数。
type Authorizer = tlsconfig.Authorizer

// AuthorizeAny 允许任意对端 SPIFFE ID(仍须通过信任域 bundle 校验证书链)。
func AuthorizeAny() Authorizer { return tlsconfig.AuthorizeAny() }

// AuthorizeID 仅允许指定 SPIFFE ID。
func AuthorizeID(id string) (Authorizer, error) {
	sid, err := parseID(id)
	if err != nil {
		return nil, err
	}
	return tlsconfig.AuthorizeID(sid), nil
}

// MustAuthorizeID 同 AuthorizeID,解析失败时 panic。
func MustAuthorizeID(id string) Authorizer {
	a, err := AuthorizeID(id)
	if err != nil {
		panic(err)
	}
	return a
}

// AuthorizeOneOf 允许列表中任一 SPIFFE ID。
func AuthorizeOneOf(ids ...string) (Authorizer, error) {
	parsed, err := parseIDs(ids...)
	if err != nil {
		return nil, err
	}
	return tlsconfig.AuthorizeOneOf(parsed...), nil
}

// AuthorizeMemberOf 允许指定信任域内任意 SPIFFE ID。
func AuthorizeMemberOf(trustDomain string) (Authorizer, error) {
	td, err := parseTrustDomain(trustDomain)
	if err != nil {
		return nil, err
	}
	return tlsconfig.AuthorizeMemberOf(td), nil
}

// MTLSServerConfig 返回要求客户端出示 X509-SVID 的服务端 tls.Config。
// 可直接传给 grpcserver.WithTLSConfig / webserver.WithTLSConfig。
// 若需从 gRPC peer 取 SPIFFE ID,优先用 MTLSServerCredentials(见 grpc.go)。
func (s *Source) MTLSServerConfig(authorizer Authorizer) *tls.Config {
	if authorizer == nil {
		authorizer = AuthorizeAny()
	}
	return tlsconfig.MTLSServerConfig(s.x509, s.x509, authorizer)
}

// TLSServerConfig 返回仅服务端出示 SVID、不校验客户端证书的 tls.Config。
func (s *Source) TLSServerConfig() *tls.Config {
	return tlsconfig.TLSServerConfig(s.x509)
}

// MTLSClientConfig 返回客户端 mTLS tls.Config(出示本端 SVID 并校验服务端)。
func (s *Source) MTLSClientConfig(authorizer Authorizer) *tls.Config {
	if authorizer == nil {
		authorizer = AuthorizeAny()
	}
	return tlsconfig.MTLSClientConfig(s.x509, s.x509, authorizer)
}

// TLSClientConfig 返回仅校验服务端 SVID、不出示客户端证书的 tls.Config。
func (s *Source) TLSClientConfig(authorizer Authorizer) *tls.Config {
	if authorizer == nil {
		authorizer = AuthorizeAny()
	}
	return tlsconfig.TLSClientConfig(s.x509, authorizer)
}

// HTTPTransport 返回带 mTLS 的 *http.Transport,可塞进 resty.WithBaseTransport /
// resty.WithHTTPBaseTransport。
func (s *Source) HTTPTransport(authorizer Authorizer) *http.Transport {
	return &http.Transport{TLSClientConfig: s.MTLSClientConfig(authorizer)}
}
