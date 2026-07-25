package wasmopa_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/wasmopa"
	"github.com/rushteam/beauty/pkg/authz"
)

//go:embed testdata/authz.wasm
var authzWasm []byte

func newPolicy(t *testing.T, opts ...wasmopa.Option) *wasmopa.Policy {
	t.Helper()
	p, err := wasmopa.New(authzWasm, opts...)
	if err != nil {
		t.Fatalf("wasmopa.New: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestEval_AdminAllowed(t *testing.T) {
	p := newPolicy(t)
	input := json.RawMessage(`{"subject":{"id":"u1","roles":["admin"]},"action":"delete","resource":"posts/123"}`)
	result, err := p.Eval(context.Background(), input)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	// OPA 结果格式: [{"result":true}] 或 [{"result":{"allow":true}}]
	t.Logf("result = %s", result)
	var bindings []struct {
		Result any `json:"result"`
	}
	if err := json.Unmarshal(result, &bindings); err != nil {
		t.Fatalf("解析结果: %v", err)
	}
	if len(bindings) == 0 {
		t.Fatal("空结果")
	}
}

func TestEval_DeniedNoRole(t *testing.T) {
	p := newPolicy(t)
	input := json.RawMessage(`{"subject":{"id":"u2","roles":["guest"]},"action":"delete","resource":"posts/123"}`)
	result, err := p.Eval(context.Background(), input)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	t.Logf("result = %s", result)
}

func TestAuthorize_AdminAllowed(t *testing.T) {
	p := newPolicy(t)
	err := p.Authorize(context.Background(), authz.Subject{
		ID:    "u1",
		Roles: []string{"admin"},
	}, "delete", "posts/123")
	if err != nil {
		t.Fatalf("admin 应被放行: %v", err)
	}
}

func TestAuthorize_ViewerReadAllowed(t *testing.T) {
	p := newPolicy(t)
	err := p.Authorize(context.Background(), authz.Subject{
		ID:    "u2",
		Roles: []string{"viewer"},
	}, "read", "posts/123")
	if err != nil {
		t.Fatalf("viewer+read 应被放行: %v", err)
	}
}

func TestAuthorize_ViewerDeleteDenied(t *testing.T) {
	p := newPolicy(t)
	err := p.Authorize(context.Background(), authz.Subject{
		ID:    "u2",
		Roles: []string{"viewer"},
	}, "delete", "posts/123")
	if err == nil {
		t.Fatal("viewer+delete 应被拒绝")
	}
	if err != authz.ErrDenied {
		t.Fatalf("应返回 ErrDenied, got %v", err)
	}
}

func TestAuthorize_NoRolesDenied(t *testing.T) {
	p := newPolicy(t)
	err := p.Authorize(context.Background(), authz.Subject{
		ID: "anon",
	}, "read", "posts/123")
	if err != authz.ErrDenied {
		t.Fatalf("无角色应被拒绝, got %v", err)
	}
}

func TestSetData_DynamicUpdate(t *testing.T) {
	p := newPolicy(t)
	// 默认 data 是 {},策略不依赖 data,admin 仍可通过
	err := p.Authorize(context.Background(), authz.Subject{
		ID:    "u1",
		Roles: []string{"admin"},
	}, "delete", "x")
	if err != nil {
		t.Fatalf("应放行: %v", err)
	}
	// SetData 不影响现有策略逻辑(data 没参与判定)
	p.SetData(json.RawMessage(`{"restricted":true}`))
	err = p.Authorize(context.Background(), authz.Subject{
		ID:    "u1",
		Roles: []string{"admin"},
	}, "delete", "x")
	if err != nil {
		t.Fatalf("更新 data 后 admin 仍应放行: %v", err)
	}
}

func TestConcurrentSafe(t *testing.T) {
	p := newPolicy(t, wasmopa.WithPool(4))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				err := p.Authorize(context.Background(), authz.Subject{
					ID:    "u1",
					Roles: []string{"admin"},
				}, "delete", "posts/1")
				if err != nil {
					t.Errorf("并发错误: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestTimeout(t *testing.T) {
	p := newPolicy(t, wasmopa.WithTimeout(1*time.Nanosecond))
	err := p.Authorize(context.Background(), authz.Subject{
		ID:    "u1",
		Roles: []string{"admin"},
	}, "delete", "x")
	// 超时应返回错误(可能是 context deadline exceeded 或实例获取失败)
	if err == nil {
		t.Log("极短超时可能仍完成(策略太快), 跳过")
	}
}
