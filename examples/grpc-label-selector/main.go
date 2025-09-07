package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	grpcclient "github.com/rushteam/beauty/pkg/client/grpc"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

func main() {
	// 创建服务发现
	discovery := etcdv3.NewRegistry(&etcdv3.Config{
		Endpoints: []string{"localhost:2379"},
	})

	// 创建客户端工厂
	factory := grpcclient.NewClientFactory(discovery)

	// 演示不同的标签过滤器用法
	if err := demonstrateBasicFiltering(factory); err != nil {
		log.Printf("基础过滤演示失败: %v", err)
	}

	if err := demonstrateAdvancedFiltering(factory); err != nil {
		log.Printf("高级过滤演示失败: %v", err)
	}

	if err := demonstrateComplexFiltering(factory); err != nil {
		log.Printf("复杂过滤演示失败: %v", err)
	}

	slog.Info("所有演示完成")
}

// demonstrateBasicFiltering 演示基础过滤
func demonstrateBasicFiltering(factory *grpcclient.ClientFactory) error {
	slog.Info("=== 基础标签过滤演示 ===")

	// 1. 精确匹配过滤
	exactMatchClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithMatchLabel("region", "us-west-1").
				WithMatchLabel("environment", "production"),
		),
	)

	// 2. 使用向后兼容的地域过滤器
	regionClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryRegionFilter(
			[]string{"us-west-1", "us-west-2"}, // regions
			[]string{},                         // zones (不限制)
			[]string{"campus-1"},               // campuses
			[]string{"production"},             // environments
		),
	)

	// 3. 使用便捷方法的多地域过滤
	multiRegionClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithRegionIn("us-west-1", "us-west-2", "us-east-1").
				WithEnvironmentIn("production", "staging"),
		),
	)

	// 测试客户端连接和服务信息获取
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 测试精确匹配客户端
	if services, err := exactMatchClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取精确匹配服务信息失败", "error", err)
	} else {
		slog.Info("精确匹配客户端", "services_found", len(services))
	}

	// 测试地域过滤客户端
	if services, err := regionClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取地域过滤服务信息失败", "error", err)
	} else {
		slog.Info("地域过滤客户端", "services_found", len(services))
	}

	// 测试多地域客户端
	if services, err := multiRegionClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取多地域服务信息失败", "error", err)
	} else {
		slog.Info("多地域客户端", "services_found", len(services))
	}

	slog.Info("基础过滤演示完成")
	return nil
}

// demonstrateAdvancedFiltering 演示高级过滤
func demonstrateAdvancedFiltering(factory *grpcclient.ClientFactory) error {
	slog.Info("=== 高级标签过滤演示 ===")

	// 1. 使用 in 操作符
	inFilterClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithExpression("tier", selector.FilterOpIn, "frontend", "api").
				WithExpression("version", selector.FilterOpIn, "v1.0", "v1.1"),
		),
	)

	// 2. 使用 notin 操作符
	notInFilterClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithExpression("tier", selector.FilterOpNotIn, "deprecated", "legacy").
				WithMatchLabel("status", "healthy"),
		),
	)

	// 3. 使用存在性检查
	existsFilterClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithExpression("canary", selector.FilterOpExists).       // 必须有 canary 标签
				WithExpression("deprecated", selector.FilterOpNotExist). // 不能有 deprecated 标签
				WithRegionIn("us-west-1"),                               // 便捷方法
		),
	)

	// 测试高级过滤客户端
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 测试 in 操作符客户端
	if services, err := inFilterClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取 in 过滤服务信息失败", "error", err)
	} else {
		slog.Info("in 操作符客户端", "services_found", len(services))
	}

	// 测试 notin 操作符客户端
	if services, err := notInFilterClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取 notin 过滤服务信息失败", "error", err)
	} else {
		slog.Info("notin 操作符客户端", "services_found", len(services))
	}

	// 测试存在性检查客户端
	if services, err := existsFilterClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取存在性过滤服务信息失败", "error", err)
	} else {
		slog.Info("存在性检查客户端", "services_found", len(services))
	}

	slog.Info("高级过滤演示完成")
	return nil
}

// demonstrateComplexFiltering 演示复杂过滤场景
func demonstrateComplexFiltering(factory *grpcclient.ClientFactory) error {
	slog.Info("=== 复杂标签过滤演示 ===")

	// 1. 混合使用多种过滤条件
	complexFilter := grpcclient.NewLabelFilter().
		// 精确匹配
		WithMatchLabel("service", "user-service").
		WithMatchLabel("status", "healthy").
		// 地域过滤（便捷方法）
		WithRegionIn("us-west-1", "us-west-2").
		WithEnvironmentIn("production").
		// 高级表达式
		WithExpression("version", selector.FilterOpIn, "v2.0", "v2.1", "v2.2").
		WithExpression("tier", selector.FilterOpNotIn, "deprecated").
		WithExpression("feature-flag", selector.FilterOpExists).
		WithExpression("maintenance", selector.FilterOpNotExist)

	complexClient := factory.GetClient("v1alpha.UserService",
		grpcclient.WithDiscoveryLabelFilter(complexFilter),
	)

	// 2. 使用客户端管理器进行复杂过滤
	managerFilter := grpcclient.NewLabelFilter().
		WithMatchLabels(map[string]string{
			"service":     "order-service",
			"environment": "production",
		}).
		WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1").
		WithExpression("load", selector.FilterOpNotEquals, "high").
		WithExpression("healthy", selector.FilterOpExists)

	manager := grpcclient.NewClientManager(factory.GetDiscovery(), "v1alpha.OrderService",
		grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
		grpcclient.WithManagerLabelFilter(managerFilter),
		grpcclient.WithHealthCheck(true, time.Second*30),
		grpcclient.WithFailover(true, 3, time.Second),
	)

	// 测试复杂过滤客户端
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 测试复杂过滤客户端
	if services, err := complexClient.GetServiceInfo(ctx); err != nil {
		slog.Warn("获取复杂过滤服务信息失败", "error", err)
	} else {
		slog.Info("复杂过滤客户端", "services_found", len(services), "filter", complexFilter.String())
	}

	// 启动客户端管理器
	if err := manager.Start(ctx); err != nil {
		slog.Error("启动客户端管理器失败", "error", err)
		return err
	}

	// 测试管理器状态
	slog.Info("管理器启动成功", "filter", managerFilter.String())

	slog.Info("复杂过滤演示完成")
	return nil
}
