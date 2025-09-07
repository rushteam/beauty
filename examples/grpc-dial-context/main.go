package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/utils/selector"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// 模拟 protobuf 生成的消息类型
type HelloRequest struct {
	Name string
}

type HelloReply struct {
	Message string
}

func main() {
	slog.Info("演示 DialContext 简化 API")

	// 演示不同的拨号方式
	demonstrateBasicDial()
	demonstrateAdvancedDial()
	demonstrateCompatibilityDial()

	slog.Info("所有演示完成")
}

// demonstrateBasicDial 演示基础拨号
func demonstrateBasicDial() {
	slog.Info("=== 基础拨号演示 ===")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// 1. 使用显式注册中心的基础拨号
	etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "/beauty",
		TTL:       10,
	})

	conn1, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter",
		grpcclient.WithRegistry(etcdRegistry),
	)
	if err != nil {
		slog.Warn("基础拨号失败", "error", err)
	} else {
		defer conn1.Close()
		slog.Info("基础拨号成功", "target", "beauty://v1alpha.Greeter")
	}

	// 2. 带环境参数的拨号
	conn2, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService?env=production",
		grpcclient.WithRegistry(etcdRegistry),
	)
	if err != nil {
		slog.Warn("环境拨号失败", "error", err)
	} else {
		defer conn2.Close()
		slog.Info("环境拨号成功", "target", "beauty://v1alpha.UserService?env=production")
	}

	// 3. 带多个参数的拨号
	conn3, err := grpcclient.DialContext(ctx, "beauty://v1alpha.OrderService?env=production&region=us-west-1&tier=frontend",
		grpcclient.WithRegistry(etcdRegistry),
	)
	if err != nil {
		slog.Warn("多参数拨号失败", "error", err)
	} else {
		defer conn3.Close()
		slog.Info("多参数拨号成功", "params", "env=production&region=us-west-1&tier=frontend")
	}
}

// demonstrateAdvancedDial 演示高级拨号
func demonstrateAdvancedDial() {
	slog.Info("=== 高级拨号演示 ===")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// 1. 使用自定义注册中心
	etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "/beauty",
		TTL:       10,
	})

	conn1, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter",
		grpcclient.WithRegistry(etcdRegistry),
		grpcclient.WithTimeout(time.Second*5),
		grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		slog.Warn("自定义注册中心拨号失败", "error", err)
	} else {
		defer conn1.Close()
		slog.Info("自定义注册中心拨号成功")
	}

	// 2. 使用高级标签过滤器
	labelFilter := grpcclient.NewLabelFilter().
		WithMatchLabel("environment", "production").
		WithExpression("tier", selector.FilterOpIn, "frontend", "api").
		WithExpression("deprecated", selector.FilterOpNotExist)

	conn2, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
		grpcclient.WithRegistry(etcdRegistry),
		grpcclient.WithLabelFilter(labelFilter),
		grpcclient.WithLoadBalancer("weighted_round_robin"),
	)
	if err != nil {
		slog.Warn("高级过滤器拨号失败", "error", err)
	} else {
		defer conn2.Close()
		slog.Info("高级过滤器拨号成功", "filter", labelFilter.String())
	}

	// 3. 使用 etcd 注册中心（需要显式提供）
	conn3, err := grpcclient.DialContext(ctx, "beauty://v1alpha.PaymentService",
		grpcclient.WithRegistry(etcdRegistry),
		grpcclient.WithEnvironment("production"),
		grpcclient.WithRegion("us-west-1"),
	)
	if err != nil {
		slog.Warn("etcd 注册中心拨号失败", "error", err)
	} else {
		defer conn3.Close()
		slog.Info("etcd 注册中心拨号成功")
	}

	// 4. 演示错误处理 - 不支持的 scheme
	_, err = grpcclient.DialContext(ctx, "unsupported://service")
	if err != nil {
		slog.Info("预期的错误", "error", err.Error()) // 这是预期的错误
	}
}

// demonstrateCompatibilityDial 演示向后兼容的拨号方式
func demonstrateCompatibilityDial() {
	slog.Info("=== 向后兼容拨号演示 ===")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// 创建注册中心
	etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "/beauty",
		TTL:       10,
	})

	// 使用向后兼容的地域过滤器
	conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter",
		grpcclient.WithRegistry(etcdRegistry),
		grpcclient.WithRegionFilter(
			[]string{"us-west-1", "us-west-2"}, // regions
			[]string{"us-west-1a"},             // zones
			[]string{"campus-1"},               // campuses
			[]string{"production"},             // environments
		),
		grpcclient.WithLoadBalancer("p2c_ewma"),
	)
	if err != nil {
		slog.Warn("兼容性拨号失败", "error", err)
	} else {
		defer conn.Close()
		slog.Info("兼容性拨号成功", "regions", "us-west-1,us-west-2")
	}
}

// demonstrateActualCall 演示实际的服务调用
func demonstrateActualCall() {
	slog.Info("=== 实际服务调用演示 ===")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// 拨号连接
	conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter?env=production",
		grpcclient.WithTimeout(time.Second*5),
	)
	if err != nil {
		slog.Error("拨号失败", "error", err)
		return
	}
	defer conn.Close()

	// 模拟调用（实际使用中会用 protobuf 生成的客户端）
	req := &HelloRequest{Name: "World"}
	resp := &HelloReply{}

	err = conn.Invoke(ctx, "/v1alpha.Greeter/SayHello", req, resp)
	if err != nil {
		slog.Warn("服务调用失败", "error", err)
	} else {
		slog.Info("服务调用成功", "response", resp.Message)
	}
}

// comparePolarisStyle 对比 Polaris 风格的用法
func comparePolarisStyle() {
	slog.Info("=== 与 Polaris 风格对比 ===")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	slog.Info("Polaris 风格:")
	slog.Info("  conn, err := polaris.DialContext(ctx, \"polaris://QuickStartEchoServerGRPC\",")
	slog.Info("      polaris.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),")
	slog.Info("      polaris.WithDisableRouter(),")
	slog.Info("  )")

	slog.Info("Beauty 风格:")
	conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter?env=production",
		grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
		grpcclient.WithDisableRouter(),
	)
	if err != nil {
		slog.Warn("Beauty 风格拨号失败", "error", err)
	} else {
		defer conn.Close()
		slog.Info("Beauty 风格拨号成功")
	}

	slog.Info("两种风格的 API 几乎一致，但 Beauty 支持更多注册中心和过滤器")
}
