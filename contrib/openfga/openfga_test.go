package openfga_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/pkg/authz"

	ofga "github.com/rushteam/beauty/contrib/openfga"
)

// mockFGA 起一个假 OpenFGA:/check 端点按请求里的 relation 决定 allowed(read→true,其它→false),
// 并记录最后一次请求体,供断言映射。
func mockFGA(t *testing.T, lastBody *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/check") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		*lastBody = string(b)
		allowed := strings.Contains(*lastBody, `"relation":"read"`)
		w.Header().Set("Content-Type", "application/json")
		if allowed {
			_, _ = io.WriteString(w, `{"allowed":true}`)
		} else {
			_, _ = io.WriteString(w, `{"allowed":false}`)
		}
	}))
}

func TestOpenFGA_Authorize(t *testing.T) {
	var body string
	srv := mockFGA(t, &body)
	defer srv.Close()

	// StoreId 用合法 ULID。
	e, err := ofga.New(srv.URL, "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	// read → allowed;并验证默认映射 user="user:alice"、object 原样。
	if err := e.Authorize(ctx, authz.Subject{ID: "alice"}, "read", "document:1"); err != nil {
		t.Fatalf("read 应放行: %v", err)
	}
	if !strings.Contains(body, `"user":"user:alice"`) || !strings.Contains(body, `"object":"document:1"`) {
		t.Fatalf("映射未按 user:<id>/object 透传: %s", body)
	}

	// write → 假服务返回 allowed=false → ErrDenied。
	err = e.Authorize(ctx, authz.Subject{ID: "alice"}, "write", "document:1")
	if !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("write 应拒绝为 ErrDenied, got %v", err)
	}
}

func TestImplementsEnforcer(t *testing.T) {
	e, err := ofga.New("http://127.0.0.1:1", "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var _ authz.Enforcer = e
}
