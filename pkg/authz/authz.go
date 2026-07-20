// Package authz 提供授权(authorization)机制:在认证(pkg/middleware/auth、pkg/token 确认
// "你是谁 + 有哪些角色")之上,判定"这个身份能否对某资源做某动作"。
//
// 分层:
//   - Subject:主体(id + 角色 + 属性),由认证层放进 context;
//   - Enforcer:决策接口,Authorize(sub, action, resource) → nil 放行 / ErrDenied 拒绝;
//   - 内置 RBAC(NewRBAC):零依赖、支持通配的角色→权限模型,覆盖多数场景;
//   - 中间件:HTTP / gRPC 拦截器,从 context 取 Subject、按 mapper 推出 (action, resource)、
//     调 Authorize,拒绝即 403 / PermissionDenied。
//
// 更复杂的策略(ABAC、动态/DB 策略、关系授权 ReBAC)由实现同一 Enforcer 接口的 contrib
// 模块提供(contrib/casbin、contrib/openfga),调用点不变。
//
// 边界(机制而非策略):策略内容、角色分配(谁是 admin)、资源命名、租户模型都由使用方定。
package authz

import (
	"context"
	"errors"
	"slices"
)

// Subject 是被授权的主体。ID 是身份;Roles 供 RBAC;Attrs 供属性判定(tenant/dept 等)。
type Subject struct {
	ID    string
	Roles []string
	Attrs map[string]string
}

// ErrDenied 表示拒绝授权。Enforcer 拒绝时返回它(或包装它)。
var ErrDenied = errors.New("authz: permission denied")

// Enforcer 是授权决策接口。Authorize 返回 nil 表示放行,返回(包装了)ErrDenied 表示拒绝。
// 各引擎(内置 RBAC、casbin、openfga)实现它,应用面向接口编程、可无缝替换。
type Enforcer interface {
	Authorize(ctx context.Context, sub Subject, action, resource string) error
}

type subjectKey struct{}

// ContextWithSubject 把主体放入 context(通常在认证中间件里,验完 token 后调用)。
func ContextWithSubject(ctx context.Context, s Subject) context.Context {
	return context.WithValue(ctx, subjectKey{}, s)
}

// SubjectFromContext 取出主体;未认证(无主体)时 ok=false。
func SubjectFromContext(ctx context.Context) (Subject, bool) {
	s, ok := ctx.Value(subjectKey{}).(Subject)
	return s, ok
}

// HasRole 报告主体是否具备某角色。
func (s Subject) HasRole(role string) bool {
	return slices.Contains(s.Roles, role)
}
