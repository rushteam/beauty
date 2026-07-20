package casbin_test

import (
	"context"
	"errors"
	"testing"

	casbinlib "github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"

	"github.com/rushteam/beauty/pkg/authz"

	casbinx "github.com/rushteam/beauty/contrib/casbin"
)

// 权限模型:sub(角色)对 obj 做 act;obj 用 keyMatch 支持 /* 通配。
const modelText = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = r.sub == p.sub && keyMatch(r.obj, p.obj) && r.act == p.act
`

func newEnforcer(t *testing.T) *casbinx.Enforcer {
	t.Helper()
	m, err := model.NewModelFromString(modelText)
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	e, err := casbinlib.NewEnforcer(m)
	if err != nil {
		t.Fatalf("enforcer: %v", err)
	}
	_, _ = e.AddPolicy("editor", "/article/*", "write")
	_, _ = e.AddPolicy("user", "/article/*", "read")
	return casbinx.New(e)
}

func TestCasbin_Authorize(t *testing.T) {
	e := newEnforcer(t)
	ctx := context.Background()
	cases := []struct {
		roles       []string
		action, obj string
		allow       bool
	}{
		{[]string{"editor"}, "write", "/article/42", true},
		{[]string{"user"}, "read", "/article/42", true},
		{[]string{"user"}, "write", "/article/42", false},         // user 无 write
		{[]string{"editor"}, "read", "/article/42", false},        // editor 无 read
		{[]string{"guest"}, "read", "/article/42", false},         // 无策略角色
		{[]string{"user", "editor"}, "write", "/article/1", true}, // 多角色任一命中
		{[]string{"user"}, "read", "/comment/1", false},           // 资源不匹配
	}
	for _, c := range cases {
		err := e.Authorize(ctx, authz.Subject{Roles: c.roles}, c.action, c.obj)
		if (err == nil) != c.allow {
			t.Errorf("roles=%v %s %s: allow=%v err=%v", c.roles, c.action, c.obj, c.allow, err)
		}
		if err != nil && !errors.Is(err, authz.ErrDenied) {
			t.Errorf("拒绝应是 authz.ErrDenied, got %v", err)
		}
	}
}

// 实现了 authz.Enforcer(编译期已断言,这里显式记录)。
func TestImplementsEnforcer(t *testing.T) {
	var _ authz.Enforcer = newEnforcer(t)
}
