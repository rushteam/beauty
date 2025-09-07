package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	grpcclient "github.com/rushteam/beauty/pkg/client/grpcclient"
	"github.com/rushteam/beauty/pkg/service/discover"

	// 导入各个服务发现包以触发自动注册
	_ "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	_ "github.com/rushteam/beauty/pkg/service/discover/k8s"
	_ "github.com/rushteam/beauty/pkg/service/discover/nacos"
	_ "github.com/rushteam/beauty/pkg/service/discover/polaris"
)

func main() {
	slog.Info("=== gRPC 注册中心插件机制演示 ===")

	// 演示1: 查看可用的注册中心方案
	demonstrateAvailableSchemes()

	// 演示2: 使用插件机制创建注册中心
	demonstrateRegistryCreation()

	// 演示3: 使用 DialContext 连接服务
	demonstrateDialContext()
}

// demonstrateAvailableSchemes 演示可用的注册中心方案
func demonstrateAvailableSchemes() {
	slog.Info("--- 演示1: 查看可用的注册中心方案 ---")

	manager := discover.GetManager()
	schemes := manager.GetAvailableSchemes()

	slog.Info("可用的注册中心方案", "schemes", schemes)

	// 检查特定方案是否支持
	testSchemes := []string{"etcd", "nacos", "polaris", "k8s", "unknown"}
	for _, scheme := range testSchemes {
		supported := manager.IsSchemeSupported(scheme)
		slog.Info("方案支持检查", "scheme", scheme, "supported", supported)
	}
}

// demonstrateRegistryCreation 演示注册中心创建
func demonstrateRegistryCreation() {
	slog.Info("--- 演示2: 使用插件机制创建注册中心 ---")

	manager := discover.GetManager()

	// 测试不同的注册中心URL
	testURLs := []string{
		"etcd://127.0.0.1:2379",
		"nacos://127.0.0.1:8848",
		"polaris://127.0.0.1:8091",
		"k8s://kubernetes.default.svc.cluster.local",
		"unknown://example.com", // 不支持的方案
	}

	for _, url := range testURLs {
		slog.Info("尝试创建注册中心", "url", url)

		registry, err := manager.CreateRegistry(url)
		if err != nil {
			slog.Error("创建注册中心失败", "url", url, "error", err)
		} else {
			slog.Info("创建注册中心成功", "url", url, "type", fmt.Sprintf("%T", registry))
		}
	}
}

// demonstrateDialContext 演示使用 DialContext 连接服务
func demonstrateDialContext() {
	slog.Info("--- 演示3: 使用 DialContext 连接服务 ---")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 测试不同的连接目标
	testTargets := []string{
		"etcd://127.0.0.1:2379/v1alpha.UserService",
		"nacos://127.0.0.1:8848/v1alpha.UserService?env=production",
		"polaris://127.0.0.1:8091/v1alpha.UserService?region=us-west-1",
		"k8s://kubernetes.default.svc.cluster.local/v1alpha.UserService",
	}

	for _, target := range testTargets {
		slog.Info("尝试连接服务", "target", target)

		conn, err := grpcclient.DialContext(ctx, target)
		if err != nil {
			slog.Error("连接服务失败", "target", target, "error", err)
		} else {
			slog.Info("连接服务成功", "target", target)
			conn.Close()
		}
	}
}
