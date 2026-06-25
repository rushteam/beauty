// Package repository 定义领域端口（接口）。
// 端口由消费者（application 层）定义、由 infra 层实现——依赖指向圆心。
package repository

import (
	"context"

	"{{.ImportPath}}internal/domain"
)

// UserRepository 用户仓储端口
type UserRepository interface {
	Create(ctx context.Context, u *domain.User) error
	Get(ctx context.Context, id string) (*domain.User, error)
	List(ctx context.Context) ([]*domain.User, error)
}
