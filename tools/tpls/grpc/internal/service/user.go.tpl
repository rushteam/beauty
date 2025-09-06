package service

import (
	"context"
	"log/slog"
	"time"

	"{{.ImportPath}}api/v1"
	"{{.ImportPath}}internal/config"
)

// UserService 用户服务
type UserService struct {
	v1.UnimplementedUserServiceServer
	cfg *config.Config
}

// NewUserService 创建用户服务
func NewUserService(cfg *config.Config) *UserService {
	return &UserService{cfg: cfg}
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, req *v1.CreateUserRequest) (*v1.CreateUserResponse, error) {
	slog.Info("创建用户", "name", req.Name, "email", req.Email)
	
	// 这里实现具体的业务逻辑
	// 例如：验证输入、保存到数据库等
	
	now := time.Now().Unix()
	user := &v1.User{
		Id:        "user-123",
		Name:      req.Name,
		Email:     req.Email,
		CreatedAt: now,
		UpdatedAt: now,
	}
	
	return &v1.CreateUserResponse{
		User: user,
	}, nil
}

// GetUser 获取用户
func (s *UserService) GetUser(ctx context.Context, req *v1.GetUserRequest) (*v1.GetUserResponse, error) {
	slog.Info("获取用户", "id", req.Id)
	
	// 这里实现具体的业务逻辑
	// 例如：从数据库查询用户信息
	
	now := time.Now().Unix()
	user := &v1.User{
		Id:        req.Id,
		Name:      "John Doe",
		Email:     "john@example.com",
		CreatedAt: now - 86400, // 1天前
		UpdatedAt: now,
	}
	
	return &v1.GetUserResponse{
		User: user,
	}, nil
}

// ListUsers 列出用户
func (s *UserService) ListUsers(ctx context.Context, req *v1.ListUsersRequest) (*v1.ListUsersResponse, error) {
	slog.Info("列出用户", "page", req.Page, "size", req.Size)
	
	// 这里实现具体的业务逻辑
	// 例如：分页查询用户列表
	
	now := time.Now().Unix()
	users := []*v1.User{
		{
			Id:        "1",
			Name:      "Alice",
			Email:     "alice@example.com",
			CreatedAt: now - 86400,
			UpdatedAt: now,
		},
		{
			Id:        "2",
			Name:      "Bob",
			Email:     "bob@example.com",
			CreatedAt: now - 172800,
			UpdatedAt: now,
		},
	}
	
	return &v1.ListUsersResponse{
		Users: users,
		Total: int32(len(users)),
	}, nil
}

// UpdateUser 更新用户
func (s *UserService) UpdateUser(ctx context.Context, req *v1.UpdateUserRequest) (*v1.UpdateUserResponse, error) {
	slog.Info("更新用户", "id", req.Id, "name", req.Name, "email", req.Email)
	
	// 这里实现具体的业务逻辑
	// 例如：验证输入、更新数据库等
	
	now := time.Now().Unix()
	user := &v1.User{
		Id:        req.Id,
		Name:      req.Name,
		Email:     req.Email,
		CreatedAt: now - 86400, // 假设1天前创建
		UpdatedAt: now,
	}
	
	return &v1.UpdateUserResponse{
		User: user,
	}, nil
}

// DeleteUser 删除用户
func (s *UserService) DeleteUser(ctx context.Context, req *v1.DeleteUserRequest) (*v1.DeleteUserResponse, error) {
	slog.Info("删除用户", "id", req.Id)
	
	// 这里实现具体的业务逻辑
	// 例如：从数据库删除用户
	
	return &v1.DeleteUserResponse{
		Success: true,
	}, nil
}
