// Package domain 是 Ring 1（领域层）：实体、值对象与纯业务规则，
// 不依赖 application / adapter / infra，任何外部框架。
package domain

import (
	"errors"
	"time"
)

// ErrInvalidUser 领域校验错误
var ErrInvalidUser = errors.New("invalid user: name and email are required")

// User 用户实体
type User struct {
	ID        string
	Name      string
	Email     string
	CreatedAt time.Time
}

// Validate 领域规则：用户名与邮箱必填（纯函数，无外部依赖）
func (u User) Validate() error {
	if u.Name == "" || u.Email == "" {
		return ErrInvalidUser
	}
	return nil
}
