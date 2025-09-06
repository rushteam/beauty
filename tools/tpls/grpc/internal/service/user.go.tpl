package service

import (
	"context"
	"log/slog"

	"{{.ImportPath}}internal/config"
)

// UserService 用户服务
type UserService struct {
	cfg *config.Config
}

// NewUserService 创建用户服务
func NewUserService(cfg *config.Config) *UserService {
	return &UserService{cfg: cfg}
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, req *CreateUserRequest) (*CreateUserResponse, error) {
	slog.Info("创建用户", "name", req.Name, "email", req.Email)
	
	// 这里实现具体的业务逻辑
	// 例如：验证输入、保存到数据库等
	
	return &CreateUserResponse{
		Id:    "user-123",
		Name:  req.Name,
		Email: req.Email,
	}, nil
}

// GetUser 获取用户
func (s *UserService) GetUser(ctx context.Context, req *GetUserRequest) (*GetUserResponse, error) {
	slog.Info("获取用户", "id", req.Id)
	
	// 这里实现具体的业务逻辑
	// 例如：从数据库查询用户信息
	
	return &GetUserResponse{
		Id:    req.Id,
		Name:  "John Doe",
		Email: "john@example.com",
	}, nil
}

// ListUsers 列出用户
func (s *UserService) ListUsers(ctx context.Context, req *ListUsersRequest) (*ListUsersResponse, error) {
	slog.Info("列出用户", "page", req.Page, "size", req.Size)
	
	// 这里实现具体的业务逻辑
	// 例如：分页查询用户列表
	
	users := []*User{
		{Id: "1", Name: "Alice", Email: "alice@example.com"},
		{Id: "2", Name: "Bob", Email: "bob@example.com"},
	}
	
	return &ListUsersResponse{
		Users: users,
		Total: int32(len(users)),
	}, nil
}

// 请求和响应结构体定义
type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateUserResponse struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type GetUserRequest struct {
	Id string `json:"id"`
}

type GetUserResponse struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ListUsersRequest struct {
	Page int32 `json:"page"`
	Size int32 `json:"size"`
}

type ListUsersResponse struct {
	Users []*User `json:"users"`
	Total int32   `json:"total"`
}

type User struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
