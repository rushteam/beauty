package metadata_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/pkg/metadata"
)

func TestMD_SetGet(t *testing.T) {
	md := metadata.New()
	md.Set("X-Tenant-ID", "t1") // 大写键应被规范化为小写
	if got := md.Get("x-tenant-id"); got != "t1" {
		t.Errorf("want t1, got %q", got)
	}
	if got := md.Get("X-Tenant-ID"); got != "t1" {
		t.Errorf("Get with mixed case: want t1, got %q", got)
	}
	if got := md.Get("x-missing"); got != "" {
		t.Errorf("missing key should return empty string, got %q", got)
	}
}

func TestMD_Clone(t *testing.T) {
	md := metadata.New()
	md.Set(metadata.KeyTenantID, "t1")
	c := md.Clone()
	c.Set(metadata.KeyTenantID, "t2")
	// 修改克隆不影响原始
	if got := md.Get(metadata.KeyTenantID); got != "t1" {
		t.Errorf("original should be unchanged, got %q", got)
	}
}

func TestMD_Merge(t *testing.T) {
	base := metadata.New()
	base.Set(metadata.KeyTenantID, "t1")
	base.Set(metadata.KeyCaller, "svc-a")

	other := metadata.New()
	other.Set(metadata.KeyCaller, "svc-b") // 覆盖
	other.Set(metadata.KeyEnv, "prod")     // 新增

	base.Merge(other)

	if got := base.Get(metadata.KeyTenantID); got != "t1" {
		t.Errorf("tenant-id should be unchanged, got %q", got)
	}
	if got := base.Get(metadata.KeyCaller); got != "svc-b" {
		t.Errorf("caller should be overwritten to svc-b, got %q", got)
	}
	if got := base.Get(metadata.KeyEnv); got != "prod" {
		t.Errorf("env should be prod, got %q", got)
	}
}

func TestContext_RoundTrip(t *testing.T) {
	md := metadata.New()
	md.Set(metadata.KeyTenantID, "tenant-123")
	md.Set(metadata.KeyCaller, "order-service")

	ctx := metadata.NewContext(context.Background(), md)
	got := metadata.FromContext(ctx)

	if got.Get(metadata.KeyTenantID) != "tenant-123" {
		t.Errorf("tenant-id round trip failed")
	}
	if got.Get(metadata.KeyCaller) != "order-service" {
		t.Errorf("caller round trip failed")
	}
}

func TestContext_Merge(t *testing.T) {
	// 向已有 MD 的 ctx 再注入新 MD，应合并
	base := metadata.New()
	base.Set(metadata.KeyTenantID, "t1")
	ctx := metadata.NewContext(context.Background(), base)

	extra := metadata.New()
	extra.Set(metadata.KeyEnv, "staging")
	extra.Set(metadata.KeyTenantID, "t2") // 覆盖
	ctx = metadata.NewContext(ctx, extra)

	md := metadata.FromContext(ctx)
	if got := md.Get(metadata.KeyTenantID); got != "t2" {
		t.Errorf("tenant-id should be t2 after merge, got %q", got)
	}
	if got := md.Get(metadata.KeyEnv); got != "staging" {
		t.Errorf("env should be staging, got %q", got)
	}
}

func TestFromContext_EmptyReturnsNonNil(t *testing.T) {
	md := metadata.FromContext(context.Background())
	if md == nil {
		t.Error("FromContext should never return nil")
	}
	// 空 MD 可以直接 Get，不 panic
	_ = md.Get("anything")
}
