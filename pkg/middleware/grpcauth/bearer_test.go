package grpcauth_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/pkg/middleware/grpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func noopHandler(ctx context.Context, req any) (any, error) { return "ok", nil }

func callUnary(t *testing.T, interceptor grpc.UnaryServerInterceptor, md metadata.MD) (any, error) {
	t.Helper()
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}
	return interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/svc/Method"}, noopHandler)
}

func TestBearer_ValidToken(t *testing.T) {
	interceptor := grpcauth.UnaryServerInterceptor("secret-token", true)
	md := metadata.Pairs("authorization", "Bearer secret-token")
	resp, err := callUnary(t, interceptor, md)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("resp=%v", resp)
	}
}

func TestBearer_InvalidToken(t *testing.T) {
	interceptor := grpcauth.UnaryServerInterceptor("secret-token", true)
	md := metadata.Pairs("authorization", "Bearer wrong-token")
	_, err := callUnary(t, interceptor, md)
	if err == nil {
		t.Fatal("expected error")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestBearer_MissingRequired(t *testing.T) {
	interceptor := grpcauth.UnaryServerInterceptor("token", true)
	_, err := callUnary(t, interceptor, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestBearer_MissingOptional(t *testing.T) {
	interceptor := grpcauth.UnaryServerInterceptor("token", false)
	resp, err := callUnary(t, interceptor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("resp=%v", resp)
	}
}

func TestBearer_NoAuthHeaderOptional(t *testing.T) {
	interceptor := grpcauth.UnaryServerInterceptor("token", false)
	md := metadata.Pairs("other-key", "value")
	resp, err := callUnary(t, interceptor, md)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("resp=%v", resp)
	}
}

func TestBearer_NoBearerPrefix(t *testing.T) {
	interceptor := grpcauth.UnaryServerInterceptor("raw-token", true)
	md := metadata.Pairs("authorization", "raw-token")
	_, err := callUnary(t, interceptor, md)
	if err == nil {
		t.Fatal("expected error for missing Bearer prefix")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestStreamBearer_Valid(t *testing.T) {
	interceptor := grpcauth.StreamServerInterceptor("tok", true)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer tok"))
	called := false
	err := interceptor(nil, &fakeStream{ctx: ctx}, &grpc.StreamServerInfo{}, func(_ any, _ grpc.ServerStream) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("handler not called")
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeStream) Context() context.Context { return f.ctx }
