// Package casbin 用 casbin/v2 实现 beauty 的 authz.Enforcer,支持 RBAC(含角色继承/域)、ABAC、
// 以及策略文件 / DB adapter 等 casbin 的全部模型能力。作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/casbin)。应用面向 authz.Enforcer 编程,可在内置 RBAC 与
// casbin 之间无缝替换。
//
// 映射:默认把 authz.Subject 的**每个角色**分别作为 casbin 请求主体去 Enforce(role, resource, action),
// 任一放行即放行——契合"角色来自 token、casbin 存权限(p 规则)"。也可用 WithSubjectID 改成用
// Subject.ID 作主体(由 casbin 自己的 g 规则解析角色),或 WithMapper 完全自定义(ABAC:带上属性)。
package casbin

import (
	"context"

	"github.com/casbin/casbin/v2"

	"github.com/rushteam/beauty/pkg/authz"
)

// Enforcer 用 casbin 实现 authz.Enforcer。
type Enforcer struct {
	e      *casbin.Enforcer
	mapper Mapper
}

// Mapper 把一次授权请求映射成若干组 casbin Enforce 参数;任一组 Enforce 为真即放行。
type Mapper func(sub authz.Subject, action, resource string) [][]any

// Option 配置 Enforcer。
type Option func(*Enforcer)

// WithMapper 自定义请求映射(如 ABAC:把 Subject.Attrs 一并传给 casbin)。
func WithMapper(m Mapper) Option { return func(e *Enforcer) { e.mapper = m } }

// WithSubjectID 用 Subject.ID 作 casbin 主体(角色由 casbin 的 g 分组策略解析),而非按角色逐个 Enforce。
func WithSubjectID() Option {
	return func(e *Enforcer) {
		e.mapper = func(sub authz.Subject, action, resource string) [][]any {
			return [][]any{{sub.ID, resource, action}}
		}
	}
}

// New 用一个已构造的 *casbin.Enforcer 包装成 authz.Enforcer。
func New(e *casbin.Enforcer, opts ...Option) *Enforcer {
	en := &Enforcer{e: e, mapper: perRoleMapper}
	for _, o := range opts {
		o(en)
	}
	return en
}

// NewFromFiles 从 casbin 模型文件 + 策略文件构造。
func NewFromFiles(modelPath, policyPath string, opts ...Option) (*Enforcer, error) {
	e, err := casbin.NewEnforcer(modelPath, policyPath)
	if err != nil {
		return nil, err
	}
	return New(e, opts...), nil
}

var _ authz.Enforcer = (*Enforcer)(nil)

// Casbin 返回底层 *casbin.Enforcer,供加载策略、管理角色、热更等高级操作。
func (e *Enforcer) Casbin() *casbin.Enforcer { return e.e }

// Authorize 实现 authz.Enforcer:按 mapper 生成的每组参数 Enforce,任一放行即放行,否则 ErrDenied。
func (e *Enforcer) Authorize(_ context.Context, sub authz.Subject, action, resource string) error {
	for _, args := range e.mapper(sub, action, resource) {
		ok, err := e.e.Enforce(args...)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return authz.ErrDenied
}

// perRoleMapper:默认映射,Subject 的每个角色各成一组 (role, resource, action)。
func perRoleMapper(sub authz.Subject, action, resource string) [][]any {
	out := make([][]any, 0, len(sub.Roles))
	for _, r := range sub.Roles {
		out = append(out, []any{r, resource, action})
	}
	return out
}
