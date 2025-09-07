package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func main() {
	slog.Info("=== 简单的注册中心插件机制演示 ===")

	// 演示1: 查看可用的注册中心方案
	demonstrateAvailableSchemes()

	// 演示2: 手动注册一个测试工厂
	demonstrateManualRegistration()
}

// demonstrateAvailableSchemes 演示可用的注册中心方案
func demonstrateAvailableSchemes() {
	slog.Info("--- 演示1: 查看可用的注册中心方案 ---")

	manager := discover.GetManager()
	schemes := manager.GetAvailableSchemes()

	slog.Info("可用的注册中心方案", "schemes", schemes)

	// 检查特定方案是否支持
	testSchemes := []string{"etcd", "nacos", "polaris", "k8s", "test", "unknown"}
	for _, scheme := range testSchemes {
		supported := manager.IsSchemeSupported(scheme)
		slog.Info("方案支持检查", "scheme", scheme, "supported", supported)
	}
}

// demonstrateManualRegistration 演示手动注册
func demonstrateManualRegistration() {
	slog.Info("--- 演示2: 手动注册测试工厂 ---")

	manager := discover.GetManager()

	// 注册一个测试工厂
	manager.RegisterFactoryFunc("test", func(targetURL *url.URL) (discover.Discovery, error) {
		slog.Info("创建测试注册中心", "url", targetURL.String())
		return &testRegistry{url: targetURL.String()}, nil
	})

	// 再次查看可用的方案
	schemes := manager.GetAvailableSchemes()
	slog.Info("注册后的可用方案", "schemes", schemes)

	// 测试创建注册中心
	registry, err := manager.CreateRegistry("test://example.com")
	if err != nil {
		slog.Error("创建测试注册中心失败", "error", err)
	} else {
		slog.Info("创建测试注册中心成功", "type", fmt.Sprintf("%T", registry))
	}
}

// testRegistry 测试用的注册中心实现
type testRegistry struct {
	url string
}

func (r *testRegistry) Register(ctx context.Context, service discover.Service) (context.CancelFunc, error) {
	slog.Info("测试注册中心注册服务", "url", r.url, "service", service.Name())
	return func() {
		slog.Info("测试注册中心注销服务", "url", r.url, "service", service.Name())
	}, nil
}

func (r *testRegistry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	slog.Info("测试注册中心查找服务", "url", r.url, "service", serviceName)
	return []discover.ServiceInfo{}, nil
}

func (r *testRegistry) Watch(ctx context.Context, serviceName string, notify discover.Notify) error {
	slog.Info("测试注册中心监听服务", "url", r.url, "service", serviceName)
	return nil
}
