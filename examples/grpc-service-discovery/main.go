package main

import (
	"context"
	"log"
	"log/slog"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/service/discover/nacos"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	grpcpkg "google.golang.org/grpc"
)

// 模拟protobuf生成的服务
type GreeterServer struct {
	grpcpkg.UnimplementedGreeterServer
}

func (s *GreeterServer) SayHello(ctx context.Context, req *HelloRequest) (*HelloReply, error) {
	return &HelloReply{Message: "Hello " + req.Name}, nil
}

type UserServiceServer struct {
	grpcpkg.UnimplementedUserServiceServer
}

func (s *UserServiceServer) CreateUser(ctx context.Context, req *CreateUserRequest) (*CreateUserResponse, error) {
	return &CreateUserResponse{Id: "123", Name: req.Name, Email: req.Email}, nil
}

func (s *UserServiceServer) GetUser(ctx context.Context, req *GetUserRequest) (*GetUserResponse, error) {
	return &GetUserResponse{Id: req.Id, Name: "Test User", Email: "test@example.com"}, nil
}

// 模拟protobuf消息类型
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

type GetUserRequest struct {
	Id string
}

type GetUserResponse struct {
	Id    string
	Name  string
	Email string
}

func main() {
	// 创建gRPC服务器，启用自动服务发现
	grpcServer := grpcserver.New(
		":58080",
		func(s *grpcpkg.Server) {
			// 注册多个protobuf服务
			// 注意：这里需要实际的protobuf生成的Register函数
			// RegisterGreeterServer(s, &GreeterServer{})
			// RegisterUserServiceServer(s, &UserServiceServer{})

			// 为了演示，我们手动注册服务描述符
			// 在实际使用中，这些会由protobuf生成
			s.RegisterService(&grpcpkg.ServiceDesc{
				ServiceName: "v1alpha.Greeter",
				HandlerType: (*GreeterServer)(nil),
				Methods: []grpcpkg.MethodDesc{
					{
						MethodName: "SayHello",
						Handler:    nil, // 实际使用中由protobuf生成
					},
				},
				Streams:  []grpcpkg.StreamDesc{},
				Metadata: "greeter.proto",
			})

			s.RegisterService(&grpcpkg.ServiceDesc{
				ServiceName: "v1alpha.UserService",
				HandlerType: (*UserServiceServer)(nil),
				Methods: []grpcpkg.MethodDesc{
					{
						MethodName: "CreateUser",
						Handler:    nil, // 实际使用中由protobuf生成
					},
					{
						MethodName: "GetUser",
						Handler:    nil, // 实际使用中由protobuf生成
					},
				},
				Streams:  []grpcpkg.StreamDesc{},
				Metadata: "user.proto",
			})
		},
		grpcserver.WithServiceName("my-grpc-server"),
		grpcserver.WithMetadata(map[string]string{
			"version":     "v1.0",
			"environment": "production",
		}),
		// 启用自动服务发现，会自动读取已注册的protobuf服务
		grpcserver.WithAutoServiceDiscovery(
			etcdv3.NewRegistry(&etcdv3.Config{
				Endpoints: []string{"127.0.0.1:2379"},
				Prefix:    "/beauty",
				TTL:       10,
			}),
			nacos.NewRegistry(&nacos.Config{
				Addr:      []string{"127.0.0.1:8848"},
				Cluster:   "",
				Namespace: "",
				Group:     "",
				Weight:    100,
			}),
		),
	)

	// 创建应用
	app := beauty.New(
		beauty.WithService(grpcServer),
		// 不再需要全局注册，因为自动服务发现已经处理了
	)

	slog.Info("启动gRPC服务，启用自动服务发现...")
	slog.Info("每个protobuf服务将作为独立服务注册到注册中心")

	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}
