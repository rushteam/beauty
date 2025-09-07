package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	grpcclient "github.com/rushteam/beauty/pkg/client/grpc"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/utils/selector"
	grpcpkg "google.golang.org/grpc"
)

// 模拟protobuf生成的消息类型
type HelloRequest struct {
	Name string
}

type HelloReply struct {
	Message string
}

type CreateUserRequest struct {
	Name  string
	Email string
}

type CreateUserResponse struct {
	Id    string
	Name  string
	Email string
}

func main() {
	// 创建服务发现客户端
	discovery := etcdv3.NewRegistry(&etcdv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "/beauty",
		TTL:       10,
	})

	// 也可以使用nacos
	// discovery := nacos.NewRegistry(&nacos.Config{
	// 	Addr:      []string{"127.0.0.1:8848"},
	// 	Namespace: "default",
	// 	Group:     "DEFAULT_GROUP",
	// })

	// 创建客户端工厂
	factory := grpcclient.NewClientFactory(discovery,
		// 设置默认选项 - 支持多选
		grpcclient.WithDiscoveryRegionFilter(
			[]string{"us-west-1", "us-west-2"},   // 支持多个地域
			[]string{"us-west-1a", "us-west-1b"}, // 支持多个可用区
			[]string{"campus-1", "campus-2"},     // 支持多个园区
			[]string{"production"},               // 支持多个环境
		),
	)

	// 创建特定服务的客户端
	greeterClient := factory.GetClient("v1alpha.Greeter")
	userClient := factory.GetClient("v1alpha.UserService")

	// 启动服务监听
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := factory.WatchAllServices(ctx); err != nil {
			log.Printf("watch services error: %v", err)
		}
	}()

	// 等待服务发现
	time.Sleep(time.Second * 2)

	// 调用Greeter服务
	callGreeterService(greeterClient)

	// 调用UserService
	callUserService(userClient)

	// 演示地域过滤
	demonstrateRegionFiltering(factory)

	// 保持运行
	select {}
}

func callGreeterService(client *grpcclient.ServiceDiscoveryClient) {
	conn, err := client.GetClient(context.Background())
	if err != nil {
		log.Printf("failed to get greeter client: %v", err)
		return
	}

	// 模拟调用SayHello方法
	req := &HelloRequest{Name: "World"}
	resp := &HelloReply{}

	// 注意：这里需要实际的protobuf生成的客户端
	// 在实际使用中，应该使用生成的客户端代码
	// greeterClient := v1.NewGreeterClient(conn)
	// resp, err := greeterClient.SayHello(context.Background(), req)

	// 为了演示，我们直接使用连接
	err = conn.Invoke(context.Background(), "/v1alpha.Greeter/SayHello", req, resp, grpcpkg.EmptyCallOption{})
	if err != nil {
		log.Printf("failed to call SayHello: %v", err)
		return
	}

	slog.Info("Greeter service response", "message", resp.Message)
}

func callUserService(client *grpcclient.ServiceDiscoveryClient) {
	conn, err := client.GetClient(context.Background())
	if err != nil {
		log.Printf("failed to get user client: %v", err)
		return
	}

	// 模拟调用CreateUser方法
	req := &CreateUserRequest{
		Name:  "John Doe",
		Email: "john@example.com",
	}
	resp := &CreateUserResponse{}

	err = conn.Invoke(context.Background(), "/v1alpha.UserService/CreateUser", req, resp, grpcpkg.EmptyCallOption{})
	if err != nil {
		log.Printf("failed to call CreateUser: %v", err)
		return
	}

	slog.Info("User service response", "id", resp.Id, "name", resp.Name, "email", resp.Email)
}

func demonstrateRegionFiltering(factory *grpcclient.ClientFactory) {
	// 创建不同地域的客户端
	usWestClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryRegionFilter(
			[]string{"us-west-1"},
			[]string{"us-west-1a"},
			[]string{"campus-1"},
			[]string{"production"},
		),
	)

	usEastClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryRegionFilter(
			[]string{"us-east-1"},
			[]string{"us-east-1a"},
			[]string{"campus-2"},
			[]string{"production"},
		),
	)

	// 演示新的标签过滤器 - 多选过滤
	multiRegionClient := factory.GetClient("v1alpha.Greeter",
		grpcclient.WithDiscoveryLabelFilter(
			grpcclient.NewLabelFilter().
				WithRegionIn("us-west-1", "us-east-1").                       // 同时支持东西部
				WithEnvironmentIn("production", "staging").                   // 支持生产和测试环境
				WithExpression("status", selector.FilterOpEquals, "healthy"), // 只要健康的实例
		),
	)

	// 获取服务信息
	ctx := context.Background()

	usWestServices, err := usWestClient.GetServiceInfo(ctx)
	if err != nil {
		log.Printf("failed to get us-west services: %v", err)
	} else {
		slog.Info("US West services", "count", len(usWestServices))
		for _, service := range usWestServices {
			slog.Info("Service", "addr", service.Addr, "region", service.Metadata["region"], "zone", service.Metadata["zone"])
		}
	}

	usEastServices, err := usEastClient.GetServiceInfo(ctx)
	if err != nil {
		log.Printf("failed to get us-east services: %v", err)
	} else {
		slog.Info("US East services", "count", len(usEastServices))
		for _, service := range usEastServices {
			slog.Info("Service", "addr", service.Addr, "region", service.Metadata["region"], "zone", service.Metadata["zone"], "campus", service.Metadata["campus"])
		}
	}

	// 演示多选过滤
	multiServices, err := multiRegionClient.GetServiceInfo(ctx)
	if err != nil {
		log.Printf("failed to get multi-region services: %v", err)
	} else {
		slog.Info("Multi-region services", "count", len(multiServices))
		for _, service := range multiServices {
			slog.Info("Multi-region Service",
				"addr", service.Addr,
				"region", service.Metadata["region"],
				"zone", service.Metadata["zone"],
				"campus", service.Metadata["campus"],
				"environment", service.Metadata["environment"])
		}
	}
}
