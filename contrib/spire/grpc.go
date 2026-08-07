package spire

import (
	"fmt"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// MTLSServerCredentials 返回 gRPC 服务端 mTLS 凭证(含 peer SPIFFE ID 包装)。
// 推荐用法:
//
//	grpcserver.WithGrpcServerOptions(grpc.Creds(source.MTLSServerCredentials(spire.AuthorizeAny())))
//
// 比 WithTLSConfig 更合适:可配合 UnaryServerInterceptor 从 context 取对端 ID。
func (s *Source) MTLSServerCredentials(authorizer Authorizer) credentials.TransportCredentials {
	if authorizer == nil {
		authorizer = AuthorizeAny()
	}
	return grpccredentials.MTLSServerCredentials(s.x509, s.x509, authorizer)
}

// TLSServerCredentials 返回仅服务端 TLS、不要求客户端证书的 gRPC 凭证。
func (s *Source) TLSServerCredentials() credentials.TransportCredentials {
	return grpccredentials.TLSServerCredentials(s.x509)
}

// MTLSClientCredentials 返回 gRPC 客户端 mTLS 凭证。
//
//	grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(source.MTLSClientCredentials(...)))
func (s *Source) MTLSClientCredentials(authorizer Authorizer) credentials.TransportCredentials {
	if authorizer == nil {
		authorizer = AuthorizeAny()
	}
	return grpccredentials.MTLSClientCredentials(s.x509, s.x509, authorizer)
}

// TLSClientCredentials 返回仅校验服务端 SVID 的 gRPC 客户端凭证。
func (s *Source) TLSClientCredentials(authorizer Authorizer) credentials.TransportCredentials {
	if authorizer == nil {
		authorizer = AuthorizeAny()
	}
	return grpccredentials.TLSClientCredentials(s.x509, authorizer)
}

// ServerCredsOption 返回可传给 grpcserver.WithGrpcServerOptions 的 ServerOption。
func (s *Source) ServerCredsOption(authorizer Authorizer) grpc.ServerOption {
	return grpc.Creds(s.MTLSServerCredentials(authorizer))
}

// DialCredsOption 返回可传给 grpcclient.WithGRPCDialOptions 的 DialOption。
func (s *Source) DialCredsOption(authorizer Authorizer) grpc.DialOption {
	return grpc.WithTransportCredentials(s.MTLSClientCredentials(authorizer))
}

func parseID(id string) (spiffeid.ID, error) {
	sid, err := spiffeid.FromString(id)
	if err != nil {
		return spiffeid.ID{}, fmt.Errorf("spire: invalid spiffe id %q: %w", id, err)
	}
	return sid, nil
}

func parseIDs(ids ...string) ([]spiffeid.ID, error) {
	out := make([]spiffeid.ID, 0, len(ids))
	for _, id := range ids {
		sid, err := parseID(id)
		if err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, nil
}

func parseTrustDomain(td string) (spiffeid.TrustDomain, error) {
	t, err := spiffeid.TrustDomainFromString(td)
	if err != nil {
		return spiffeid.TrustDomain{}, fmt.Errorf("spire: invalid trust domain %q: %w", td, err)
	}
	return t, nil
}
