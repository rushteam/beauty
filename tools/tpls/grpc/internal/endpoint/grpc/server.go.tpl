package grpc

import (
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/service"
	"google.golang.org/grpc"
)

// RegisterServices 注册gRPC服务
func RegisterServices(s *grpc.Server, cfg *config.Config) {
	// 创建服务实例
	_ = service.NewUserService(cfg)
	
	// 注册服务
	// 注意：这里需要根据实际的protobuf定义来注册服务
	// 例如：pb.RegisterUserServiceServer(s, userService)
	
	slog.Info("gRPC服务注册完成", "service", "UserService")
}
