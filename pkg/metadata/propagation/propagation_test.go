package propagation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rushteam/beauty/pkg/metadata"
	"github.com/rushteam/beauty/pkg/metadata/propagation"
	grpcmd "google.golang.org/grpc/metadata"
)

// ---- HTTP ---------------------------------------------------------------

func TestHTTPExtract(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tenant-ID", "t1")
	r.Header.Set("X-Caller", "svc-a")
	r.Header.Set("Content-Type", "application/json") // 非 x- 前缀，不透传

	md := propagation.HTTPExtract(r)

	if got := md.Get(metadata.KeyTenantID); got != "t1" {
		t.Errorf("tenant-id: want t1, got %q", got)
	}
	if got := md.Get(metadata.KeyCaller); got != "svc-a" {
		t.Errorf("caller: want svc-a, got %q", got)
	}
	if got := md.Get("content-type"); got != "" {
		t.Errorf("content-type should not be extracted, got %q", got)
	}
}

func TestHTTPInject(t *testing.T) {
	md := metadata.New()
	md.Set(metadata.KeyTenantID, "t1")
	md.Set(metadata.KeyEnv, "prod")
	md.Set("some-other-key", "val") // 无 x- 前缀，不注入

	h := http.Header{}
	propagation.HTTPInject(md, h)

	if got := h.Get("X-Tenant-Id"); got != "t1" {
		t.Errorf("X-Tenant-Id: want t1, got %q", got)
	}
	if got := h.Get("X-Env"); got != "prod" {
		t.Errorf("X-Env: want prod, got %q", got)
	}
	if got := h.Get("Some-Other-Key"); got != "" {
		t.Errorf("some-other-key should not be injected, got %q", got)
	}
}

func TestHTTPServerMiddleware(t *testing.T) {
	var capturedMD metadata.MD
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMD = metadata.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wrapped := propagation.HTTPServerMiddleware(handler)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tenant-ID", "tenant-abc")
	r.Header.Set("X-Request-ID", "req-123")
	r.Header.Set("Authorization", "Bearer token") // 不透传

	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)

	if capturedMD.Get(metadata.KeyTenantID) != "tenant-abc" {
		t.Errorf("tenant-id not propagated to context")
	}
	if capturedMD.Get(metadata.KeyRequestID) != "req-123" {
		t.Errorf("request-id not propagated to context")
	}
	if capturedMD.Get("authorization") != "" {
		t.Errorf("authorization should not be propagated")
	}

	// 透传字段应回写到响应 header
	if got := w.Header().Get("X-Tenant-Id"); got != "tenant-abc" {
		t.Errorf("tenant-id should be written to response header, got %q", got)
	}
}

func TestHTTPClientInject(t *testing.T) {
	md := metadata.New()
	md.Set(metadata.KeyTenantID, "t1")
	md.Set(metadata.KeyCaller, "order-svc")
	ctx := metadata.NewContext(context.Background(), md)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	propagation.HTTPClientInject(ctx, r)

	if got := r.Header.Get("X-Tenant-Id"); got != "t1" {
		t.Errorf("X-Tenant-Id: want t1, got %q", got)
	}
	if got := r.Header.Get("X-Caller"); got != "order-svc" {
		t.Errorf("X-Caller: want order-svc, got %q", got)
	}
}

func TestHTTPClientInject_EmptyMD(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// 空 context，不应 panic 也不应修改 header
	propagation.HTTPClientInject(context.Background(), r)
	if len(r.Header) != 0 {
		t.Errorf("headers should be empty for empty MD, got %v", r.Header)
	}
}

// ---- gRPC ---------------------------------------------------------------

func TestGRPCExtract(t *testing.T) {
	incoming := grpcmd.Pairs(
		"x-tenant-id", "t1",
		"x-caller", "svc-a",
		"content-type", "application/grpc", // 非 x-，不透传
	)
	ctx := grpcmd.NewIncomingContext(context.Background(), incoming)

	md := propagation.GRPCExtract(ctx)

	if got := md.Get(metadata.KeyTenantID); got != "t1" {
		t.Errorf("tenant-id: want t1, got %q", got)
	}
	if got := md.Get(metadata.KeyCaller); got != "svc-a" {
		t.Errorf("caller: want svc-a, got %q", got)
	}
	if got := md.Get("content-type"); got != "" {
		t.Errorf("content-type should not be extracted, got %q", got)
	}
}

func TestGRPCClientInject(t *testing.T) {
	md := metadata.New()
	md.Set(metadata.KeyTenantID, "t1")
	md.Set(metadata.KeyEnv, "staging")
	ctx := metadata.NewContext(context.Background(), md)

	ctx = propagation.GRPCClientInject(ctx)

	outgoing, ok := grpcmd.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("no outgoing metadata after GRPCClientInject")
	}
	if vals := outgoing.Get("x-tenant-id"); len(vals) == 0 || vals[0] != "t1" {
		t.Errorf("x-tenant-id: want t1, got %v", vals)
	}
	if vals := outgoing.Get("x-env"); len(vals) == 0 || vals[0] != "staging" {
		t.Errorf("x-env: want staging, got %v", vals)
	}
}

func TestGRPCClientInject_MergesExisting(t *testing.T) {
	// ctx 中已有 outgoing metadata，新 MD 应合并而非覆盖
	existing := grpcmd.Pairs("authorization", "Bearer token")
	ctx := grpcmd.NewOutgoingContext(context.Background(), existing)

	md := metadata.New()
	md.Set(metadata.KeyTenantID, "t1")
	ctx = metadata.NewContext(ctx, md)
	ctx = propagation.GRPCClientInject(ctx)

	outgoing, _ := grpcmd.FromOutgoingContext(ctx)
	if vals := outgoing.Get("authorization"); len(vals) == 0 {
		t.Error("existing authorization should be preserved")
	}
	if vals := outgoing.Get("x-tenant-id"); len(vals) == 0 || vals[0] != "t1" {
		t.Errorf("x-tenant-id should be added, got %v", vals)
	}
}

func TestGRPCClientInject_EmptyMD(t *testing.T) {
	ctx := propagation.GRPCClientInject(context.Background())
	if _, ok := grpcmd.FromOutgoingContext(ctx); ok {
		t.Error("empty MD should not create outgoing metadata")
	}
}

// ---- HTTP → gRPC 跨协议透传链路 -----------------------------------------

func TestCrossProtocol_HTTPToGRPC(t *testing.T) {
	// 1. HTTP 服务端从 header 提取 MD
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tenant-ID", "cross-tenant")
	r.Header.Set("X-Caller", "api-gateway")
	ctx := metadata.NewContext(context.Background(), propagation.HTTPExtract(r))

	// 2. 下游调用 gRPC 服务时注入
	ctx = propagation.GRPCClientInject(ctx)

	// 3. gRPC 服务端收到
	outgoing, ok := grpcmd.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("no outgoing metadata")
	}
	if vals := outgoing.Get("x-tenant-id"); len(vals) == 0 || vals[0] != "cross-tenant" {
		t.Errorf("x-tenant-id cross-protocol: want cross-tenant, got %v", vals)
	}
	if vals := outgoing.Get("x-caller"); len(vals) == 0 || vals[0] != "api-gateway" {
		t.Errorf("x-caller cross-protocol: want api-gateway, got %v", vals)
	}
}
