package xds

import (
	"testing"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc/resolver"
)

// TestXDSResolverRegistered 验证空导入本包后，gRPC 的 xds:// resolver 已注册。
func TestXDSResolverRegistered(t *testing.T) {
	if resolver.Get("xds") == nil {
		t.Fatal("xds resolver 未注册：应由空导入 google.golang.org/grpc/xds 完成")
	}
}

// TestDialXDSTarget 验证 xds:///service 目标能经 grpcclient 正常建链（惰性连接，
// 不实际拨号），即 dialXDS 路由与 resolver 注册均已打通。
func TestDialXDSTarget(t *testing.T) {
	conn, err := grpcclient.Dial("xds:///test-service", grpcclient.WithInsecure())
	if err != nil {
		t.Fatalf("Dial xds:///test-service 失败: %v", err)
	}
	if conn == nil {
		t.Fatal("期望返回非空 ClientConn")
	}
	_ = conn.Close()
}

// TestWithCredentials 验证 xDS 凭证选项可正常构造。
func TestWithCredentials(t *testing.T) {
	if WithCredentials() == nil {
		t.Fatal("WithCredentials 返回了 nil DialOption")
	}
}
