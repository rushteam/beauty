package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
)

func main() {
	// 创建服务发现
	discovery := etcdv3.NewRegistry(&etcdv3.Config{
		Endpoints: []string{"localhost:2379"},
	})

	// 创建客户端工厂
	factory := grpcclient.NewClientFactory(discovery)

	// 演示不同的标签过滤器用法
	demonstrateBasicFiltering(factory)
	demonstrateAdvancedFiltering(factory)
	demonstrateComplexFiltering(factory)
}

// demonstrateBasicFiltering 演示基础过滤
func demonstrateBasicFiltering(factory *grpcclient.ClientFactory) {
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

	slog.Info("创建了基础过滤客户端",
		"exactMatch", exactMatchClient != nil,
		"region", regionClient != nil,
		"multiRegion", multiRegionClient != nil)
}

// demonstrateAdvancedFiltering 演示高级过滤
func demonstrateAdvancedFiltering(factory *grpcclient.ClientFactory) {
	slog.Info("=== 高级标签过滤演示 ===")

	// 1. 使用 in 操作符
	inFilterClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithExpression("tier", grpcclient.FilterOpIn, "frontend", "api").
				WithExpression("version", grpcclient.FilterOpIn, "v1.0", "v1.1"),
		),
	)

	// 2. 使用 notin 操作符
	notInFilterClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithExpression("tier", grpcclient.FilterOpNotIn, "deprecated", "legacy").
				WithMatchLabel("status", "healthy"),
		),
	)

	// 3. 使用存在性检查
	existsFilterClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithExpression("canary", grpcclient.FilterOpExists).       // 必须有 canary 标签
				WithExpression("deprecated", grpcclient.FilterOpNotExist). // 不能有 deprecated 标签
				WithRegionIn("us-west-1"),                                 // 便捷方法
		),
	)

	slog.Info("创建了高级过滤客户端",
		"inFilter", inFilterClient != nil,
		"notInFilter", notInFilterClient != nil,
		"existsFilter", existsFilterClient != nil)
}

// demonstrateComplexFiltering 演示复杂过滤场景
func demonstrateComplexFiltering(factory *grpcclient.ClientFactory) {
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
		WithExpression("version", grpcclient.FilterOpIn, "v2.0", "v2.1", "v2.2").
		WithExpression("tier", grpcclient.FilterOpNotIn, "deprecated").
		WithExpression("feature-flag", grpcclient.FilterOpExists).
		WithExpression("maintenance", grpcclient.FilterOpNotExist)

	complexClient := factory.GetClient("v1alpha.UserService",
		grpcclient.WithDiscoveryLabelFilter(complexFilter),
	)

	// 2. 使用客户端管理器进行复杂过滤
	managerFilter := grpcclient.NewLabelFilter().
		WithMatchLabels(map[string]string{
			"service":     "order-service",
			"environment": "production",
		}).
		WithExpression("region", grpcclient.FilterOpIn, "us-west-1", "us-east-1").
		WithExpression("load", grpcclient.FilterOpNotEquals, "high").
		WithExpression("healthy", grpcclient.FilterOpExists)

	manager := grpcclient.NewClientManager(factory.GetDiscovery(), "v1alpha.OrderService",
		grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
		grpcclient.WithManagerLabelFilter(managerFilter),
		grpcclient.WithHealthCheck(true, time.Second*30),
		grpcclient.WithFailover(true, 3, time.Second),
	)

	// 启动管理器
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		slog.Error("启动客户端管理器失败", "error", err)
	}

	slog.Info("创建了复杂过滤场景",
		"complexClient", complexClient != nil,
		"manager", manager != nil,
		"filterString", complexFilter.String())
}
