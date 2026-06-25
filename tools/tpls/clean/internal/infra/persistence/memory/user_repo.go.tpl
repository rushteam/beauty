// Package memory 是 Ring 3（出站适配器）：用内存实现 domain 的仓储端口。
// infra 依赖 domain（实现其接口）合法——依赖指向圆心。
package memory

import (
	"context"
	"errors"
	"sync"

	"{{.ImportPath}}internal/domain"
	"{{.ImportPath}}internal/domain/repository"
)

// ErrNotFound 未找到用户
var ErrNotFound = errors.New("user not found")

// UserRepo 内存版用户仓储
type UserRepo struct {
	mu    sync.RWMutex
	users map[string]*domain.User
}

// NewUserRepo 创建内存用户仓储
func NewUserRepo() *UserRepo {
	return &UserRepo{users: make(map[string]*domain.User)}
}

// 编译期断言：UserRepo 实现了 domain 端口
var _ repository.UserRepository = (*UserRepo)(nil)

// Create 保存用户
func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[u.ID] = u
	return nil
}

// Get 按 ID 获取用户
func (r *UserRepo) Get(ctx context.Context, id string) (*domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	u, ok := r.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

// List 列出全部用户
func (r *UserRepo) List(ctx context.Context) ([]*domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*domain.User, 0, len(r.users))
	for _, u := range r.users {
		out = append(out, u)
	}
	return out, nil
}
