// Package user 是 Ring 2（应用层）：用例编排。
// 只依赖 domain 端口接口，禁止 import internal/infra（整洁架构核心规则）。
package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"{{.ImportPath}}internal/domain"
	"{{.ImportPath}}internal/domain/repository"
)

// Service 用户用例服务
type Service struct {
	repo repository.UserRepository
}

// New 创建用户用例服务（依赖注入端口接口，而非具体实现）
func New(repo repository.UserRepository) *Service {
	return &Service{repo: repo}
}

// Create 创建用户：组装实体 -> 领域校验 -> 持久化
func (s *Service) Create(ctx context.Context, name, email string) (*domain.User, error) {
	u := &domain.User{
		ID:        newID(),
		Name:      name,
		Email:     email,
		CreatedAt: time.Now(),
	}
	if err := u.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Get 获取用户
func (s *Service) Get(ctx context.Context, id string) (*domain.User, error) {
	return s.repo.Get(ctx, id)
}

// List 列出用户
func (s *Service) List(ctx context.Context) ([]*domain.User, error) {
	return s.repo.List(ctx)
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
