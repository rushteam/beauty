package grpc

import (
	"context"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/service"
	"google.golang.org/grpc"
)

// RegisterServices 注册gRPC服务
func RegisterServices(s *grpc.Server, cfg *config.Config) {
	// 创建服务实例
	userService := service.NewUserService(cfg)
	
	// 注册服务
	// pb.RegisterUserServiceServer(s, userService)
	
	slog.Info("gRPC服务注册完成", "service", "UserService")
}

// UserService gRPC服务实现
type UserService struct {
	cfg *config.Config
}

// NewUserService 创建用户服务
func NewUserService(cfg *config.Config) *UserService {
	return &UserService{cfg: cfg}
}

// 在这里实现gRPC方法...
// func (s *UserService) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
//     // 实现创建用户逻辑
//     return &pb.CreateUserResponse{}, nil
// }
