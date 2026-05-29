package requestid

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty/pkg/utils/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const Header = "X-Request-ID"
const metaKey = "x-request-id"

type contextKey struct{}

// FromContext extracts the request ID from ctx.
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKey{}).(string); ok {
		return v
	}
	return ""
}

// NewContext returns a copy of ctx with the given request ID attached.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// HTTPMiddleware injects or propagates X-Request-ID for HTTP handlers.
// If the incoming request already carries the header, it is reused; otherwise a new UUID is generated.
// The ID is set on the response header and injected into the request context.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if id == "" {
			id = uuid.New()
		}
		w.Header().Set(Header, id)
		next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), id)))
	})
}

// UnaryServerInterceptor propagates or generates x-request-id for gRPC unary calls.
func UnaryServerInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx = injectFromGRPCMeta(ctx)
	return handler(ctx, req)
}

// StreamServerInterceptor propagates or generates x-request-id for gRPC streaming calls.
func StreamServerInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := injectFromGRPCMeta(ss.Context())
	return handler(srv, &wrappedStream{ss, ctx})
}

func injectFromGRPCMeta(ctx context.Context) context.Context {
	id := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(metaKey); len(vals) > 0 {
			id = vals[0]
		}
	}
	if id == "" {
		id = uuid.New()
	}
	return NewContext(ctx, id)
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
