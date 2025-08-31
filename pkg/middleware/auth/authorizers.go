package auth

import (
	"context"
	"strings"
	"sync"
)

// RoleBasedAuthorizer 基于角色的授权器
type RoleBasedAuthorizer struct {
	permissions map[string][]string // resource:action -> required roles
	mutex       sync.RWMutex
}

// NewRoleBasedAuthorizer 创建基于角色的授权器
func NewRoleBasedAuthorizer() *RoleBasedAuthorizer {
	return &RoleBasedAuthorizer{
		permissions: make(map[string][]string),
	}
}

// AddPermission 添加权限规则
func (a *RoleBasedAuthorizer) AddPermission(resource, action string, roles ...string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	key := resource + ":" + action
	a.permissions[key] = roles
}

// RemovePermission 移除权限规则
func (a *RoleBasedAuthorizer) RemovePermission(resource, action string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	key := resource + ":" + action
	delete(a.permissions, key)
}

// Authorize 检查用户是否有权限
func (a *RoleBasedAuthorizer) Authorize(ctx context.Context, user User, resource, action string) error {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	key := resource + ":" + action
	requiredRoles, exists := a.permissions[key]
	if !exists {
		// 没有配置权限规则，默认允许
		return nil
	}

	// 检查用户是否拥有任一所需角色
	userRoles := user.Roles()
	for _, requiredRole := range requiredRoles {
		for _, userRole := range userRoles {
			if userRole == requiredRole {
				return nil
			}
		}
	}

	return ErrForbidden
}

// PathBasedAuthorizer 基于路径的授权器
type PathBasedAuthorizer struct {
	rules []PathRule
	mutex sync.RWMutex
}

// PathRule 路径规则
type PathRule struct {
	PathPattern string   // 路径模式，支持通配符
	Methods     []string // HTTP 方法列表，空表示所有方法
	Roles       []string // 允许的角色列表
	AllowAll    bool     // 是否允许所有用户
}

// NewPathBasedAuthorizer 创建基于路径的授权器
func NewPathBasedAuthorizer() *PathBasedAuthorizer {
	return &PathBasedAuthorizer{
		rules: make([]PathRule, 0),
	}
}

// AddRule 添加路径规则
func (a *PathBasedAuthorizer) AddRule(rule PathRule) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.rules = append(a.rules, rule)
}

// AddPublicPath 添加公开路径（无需认证）
func (a *PathBasedAuthorizer) AddPublicPath(pathPattern string, methods ...string) {
	a.AddRule(PathRule{
		PathPattern: pathPattern,
		Methods:     methods,
		AllowAll:    true,
	})
}

// AddProtectedPath 添加受保护路径
func (a *PathBasedAuthorizer) AddProtectedPath(pathPattern string, roles []string, methods ...string) {
	a.AddRule(PathRule{
		PathPattern: pathPattern,
		Methods:     methods,
		Roles:       roles,
		AllowAll:    false,
	})
}

// Authorize 检查用户是否有权限访问路径
func (a *PathBasedAuthorizer) Authorize(ctx context.Context, user User, resource, action string) error {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	// resource 是路径，action 是 HTTP 方法
	path := resource
	method := strings.ToUpper(action)

	// 查找匹配的规则
	for _, rule := range a.rules {
		if a.matchPath(rule.PathPattern, path) && a.matchMethod(rule.Methods, method) {
			if rule.AllowAll {
				return nil // 公开路径
			}

			// 检查角色权限
			if len(rule.Roles) == 0 {
				return nil // 没有角色限制
			}

			userRoles := user.Roles()
			for _, requiredRole := range rule.Roles {
				for _, userRole := range userRoles {
					if userRole == requiredRole {
						return nil
					}
				}
			}

			return ErrForbidden
		}
	}

	// 没有匹配的规则，默认拒绝
	return ErrForbidden
}

// matchPath 检查路径是否匹配模式（简单的通配符支持）
func (a *PathBasedAuthorizer) matchPath(pattern, path string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == path {
		return true
	}
	// 支持简单的前缀匹配
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(path, prefix)
	}
	return false
}

// matchMethod 检查方法是否匹配
func (a *PathBasedAuthorizer) matchMethod(allowedMethods []string, method string) bool {
	if len(allowedMethods) == 0 {
		return true // 允许所有方法
	}
	for _, allowedMethod := range allowedMethods {
		if strings.ToUpper(allowedMethod) == method {
			return true
		}
	}
	return false
}

// CallbackAuthorizer 回调授权器（允许业务方自定义授权逻辑）
type CallbackAuthorizer struct {
	AuthorizeFunc func(ctx context.Context, user User, resource, action string) error
}

// NewCallbackAuthorizer 创建回调授权器
func NewCallbackAuthorizer(authorizeFunc func(ctx context.Context, user User, resource, action string) error) *CallbackAuthorizer {
	return &CallbackAuthorizer{
		AuthorizeFunc: authorizeFunc,
	}
}

// Authorize 执行授权回调
func (a *CallbackAuthorizer) Authorize(ctx context.Context, user User, resource, action string) error {
	if a.AuthorizeFunc == nil {
		return nil // 没有授权函数，默认允许
	}
	return a.AuthorizeFunc(ctx, user, resource, action)
}

// CompositeAuthorizer 组合授权器（需要所有授权器都通过）
type CompositeAuthorizer struct {
	Authorizers []Authorizer
}

// NewCompositeAuthorizer 创建组合授权器
func NewCompositeAuthorizer(authorizers ...Authorizer) *CompositeAuthorizer {
	return &CompositeAuthorizer{
		Authorizers: authorizers,
	}
}

// Authorize 检查所有授权器
func (a *CompositeAuthorizer) Authorize(ctx context.Context, user User, resource, action string) error {
	for _, authorizer := range a.Authorizers {
		if err := authorizer.Authorize(ctx, user, resource, action); err != nil {
			return err
		}
	}
	return nil
}

// PermissiveAuthorizer 宽松授权器（总是允许）
type PermissiveAuthorizer struct{}

// NewPermissiveAuthorizer 创建宽松授权器
func NewPermissiveAuthorizer() *PermissiveAuthorizer {
	return &PermissiveAuthorizer{}
}

// Authorize 总是允许
func (a *PermissiveAuthorizer) Authorize(ctx context.Context, user User, resource, action string) error {
	return nil
}

// RestrictiveAuthorizer 严格授权器（总是拒绝）
type RestrictiveAuthorizer struct{}

// NewRestrictiveAuthorizer 创建严格授权器
func NewRestrictiveAuthorizer() *RestrictiveAuthorizer {
	return &RestrictiveAuthorizer{}
}

// Authorize 总是拒绝
func (a *RestrictiveAuthorizer) Authorize(ctx context.Context, user User, resource, action string) error {
	return ErrForbidden
}
