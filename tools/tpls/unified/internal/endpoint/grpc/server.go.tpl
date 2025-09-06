{{if .EnableGrpc}}package grpc

import (
	"log/slog"

	"{{.ImportPath}}api/v1"
	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/service"
	"google.golang.org/grpc"
)

// RegisterServices 注册gRPC服务
func RegisterServices(s *grpc.Server, cfg *config.Config) {
	// 创建服务实例
	userService := service.NewUserService(cfg)
	
	// 注册gRPC服务
	v1.RegisterUserServiceServer(s, userService)
	
	slog.Info("gRPC服务注册完成", "service", "UserService")
}
{{end}}
