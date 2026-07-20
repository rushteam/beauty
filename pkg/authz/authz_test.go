package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/rushteam/beauty/pkg/authz"
)

func rbac() *authz.RBAC {
	return authz.NewRBAC().
		Grant("admin", "*", "*").
		Grant("editor", "update", "article/*").
		Grant("editor", "create", "article/*").
		Grant("user", "read", "article/*")
}

func TestRBAC(t *testing.T) {
	e := rbac()
	cases := []struct {
		roles       []string
		action, res string
		allowed     bool
	}{
		{[]string{"admin"}, "delete", "anything/x", true},         // 通配 *,*
		{[]string{"editor"}, "update", "article/42", true},        // 前缀 article/*
		{[]string{"editor"}, "read", "article/42", false},         // editor 无 read
		{[]string{"user"}, "read", "article/42", true},            //
		{[]string{"user"}, "update", "article/42", false},         //
		{[]string{"user"}, "read", "comment/1", false},            // 资源前缀不匹配
		{[]string{"guest"}, "read", "article/1", false},           // 未知角色
		{[]string{"user", "editor"}, "update", "article/9", true}, // 多角色任一命中
	}
	for _, c := range cases {
		err := e.Authorize(context.Background(), authz.Subject{Roles: c.roles}, c.action, c.res)
		if (err == nil) != c.allowed {
			t.Errorf("roles=%v %s %s: allowed=%v, err=%v", c.roles, c.action, c.res, c.allowed, err)
		}
	}
}

func TestSubjectContext(t *testing.T) {
	ctx := authz.ContextWithSubject(context.Background(), authz.Subject{ID: "u1", Roles: []string{"admin"}})
	s, ok := authz.SubjectFromContext(ctx)
	if !ok || s.ID != "u1" || !s.HasRole("admin") || s.HasRole("x") {
		t.Fatalf("subject roundtrip: %+v ok=%v", s, ok)
	}
	if _, ok := authz.SubjectFromContext(context.Background()); ok {
		t.Fatal("空 context 不应有主体")
	}
}

func TestHTTPMiddleware(t *testing.T) {
	e := authz.NewRBAC().Grant("user", "read", "/articles")
	h := authz.HTTP(e, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	// 无主体 → 401
	if rec := serve(h, "GET", "/articles", authz.Subject{}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无主体 status=%d, want 401", rec.Code)
	}
	// 有 user 角色、GET(read)/articles → 放行
	if rec := serve(h, "GET", "/articles", authz.Subject{Roles: []string{"user"}}); rec.Code != http.StatusOK {
		t.Fatalf("放行 status=%d, want 200", rec.Code)
	}
	// user 无 delete 权限 → 403
	if rec := serve(h, "DELETE", "/articles", authz.Subject{Roles: []string{"user"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("拒绝 status=%d, want 403", rec.Code)
	}
}

func TestGRPCInterceptor(t *testing.T) {
	e := authz.NewRBAC().Grant("admin", "*", "*")
	ic := authz.UnaryServerInterceptor(e, nil)
	info := &grpc.UnaryServerInfo{FullMethod: "/user.User/Delete"}
	handler := func(context.Context, any) (any, error) { return "done", nil }

	// 无主体 → Unauthenticated
	_, err := ic(context.Background(), nil, info, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("无主体 code=%v, want Unauthenticated", status.Code(err))
	}
	// admin → 放行
	ctxAdmin := authz.ContextWithSubject(context.Background(), authz.Subject{Roles: []string{"admin"}})
	if r, err := ic(ctxAdmin, nil, info, handler); err != nil || r != "done" {
		t.Fatalf("admin 应放行: r=%v err=%v", r, err)
	}
	// 无权限角色 → PermissionDenied
	ctxUser := authz.ContextWithSubject(context.Background(), authz.Subject{Roles: []string{"user"}})
	if _, err := ic(ctxUser, nil, info, handler); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("user code=%v, want PermissionDenied", status.Code(err))
	}
}

func serve(h http.Handler, method, path string, sub authz.Subject) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, nil)
	if sub.ID != "" || len(sub.Roles) > 0 {
		r = r.WithContext(authz.ContextWithSubject(r.Context(), sub))
	}
	h.ServeHTTP(rec, r)
	return rec
}
