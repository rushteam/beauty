package handler_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/afterwork"
	perr "github.com/rushteam/beauty/pkg/errors"
	"github.com/rushteam/beauty/pkg/handler"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/ratelimit"
)

type echoReq struct{ Msg string }
type echoResp struct{ Echo string }

func okHandler() handler.Func[echoReq, echoResp] {
	return func(ctx context.Context, req *echoReq) (*echoResp, error) {
		return &echoResp{Echo: req.Msg}, nil
	}
}

func TestHandler_Success(t *testing.T) {
	h := handler.New("", okHandler())
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"Msg":"hi"}`)
	req := httptest.NewRequest("POST", "/", body)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var got echoResp
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Echo != "hi" {
		t.Fatalf("echo=%q", got.Echo)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := handler.New("POST", okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code=%d want 501", rec.Code)
	}
}

func TestHandler_InvalidBody(t *testing.T) {
	h := handler.New("", okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader("{bad"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestHandler_StatusError_Normalized(t *testing.T) {
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		return nil, perr.New(perr.CodeForbidden, "no access")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
}

func TestHandler_PlainError_Becomes500(t *testing.T) {
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		return nil, stderrors.New("boom")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d want 500", rec.Code)
	}
}

func TestHandler_AuthPolicy_InjectsUser(t *testing.T) {
	var sawUserID string
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		u, ok := auth.GetUserFromContext(ctx)
		if !ok {
			t.Fatal("user should be injected")
		}
		sawUserID = u.ID()
		return &echoResp{Echo: req.Msg}, nil
	}, handler.WithAuth(func(ctx context.Context, r *http.Request) (auth.User, error) {
		return auth.NewUser("u1", "alice", nil), nil
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"hi"}`))
	h.ServeHTTP(rec, req)
	if sawUserID != "u1" {
		t.Fatalf("sawUserID=%q", sawUserID)
	}
}

func TestHandler_AuthPolicy_FailsBeforeHandler(t *testing.T) {
	ran := false
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		ran = true
		return &echoResp{}, nil
	}, handler.WithAuth(func(ctx context.Context, r *http.Request) (auth.User, error) {
		return nil, perr.New(perr.CodeUnauthenticated, "missing token")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if ran {
		t.Fatal("handler should not run when auth fails")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

type fakeDB struct{ Name string }

func TestHandler_Inject_GetsDep(t *testing.T) {
	db := &fakeDB{Name: "primary"}
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		got := handler.MustGet[*fakeDB](ctx, "db")
		return &echoResp{Echo: got.Name}, nil
	}, handler.WithInject("db", db))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	var got echoResp
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Echo != "primary" {
		t.Fatalf("echo=%q want primary", got.Echo)
	}
}

func TestHandler_Inject_WrongType_NotFound(t *testing.T) {
	db := &fakeDB{Name: "primary"}
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		if _, ok := handler.Get[int](ctx, "db"); ok {
			t.Fatal("int should not match *fakeDB")
		}
		return &echoResp{}, nil
	}, handler.WithInject("db", db))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestHandler_NilResp_204(t *testing.T) {
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		return nil, nil
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204", rec.Code)
	}
}

func TestHandler_Afterwork_RunsAfterResponse(t *testing.T) {
	var ran atomic.Int32
	h := handler.New("", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		afterwork.Defer(ctx, func(context.Context) { ran.Add(1) })
		return &echoResp{Echo: req.Msg}, nil
	}, handler.WithAfterwork())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"hi"}`))
	h.ServeHTTP(rec, req)
	// ServeHTTP 返回时延寿任务应已跑完。
	if got := ran.Load(); got != 1 {
		t.Fatalf("deferred task should run before ServeHTTP returns, got %d", got)
	}
}

func TestHandler_NoBody_GET(t *testing.T) {
	// GET 请求无 body,handler 收到零值 req。
	h := handler.New("GET", func(ctx context.Context, req *echoReq) (*echoResp, error) {
		return &echoResp{Echo: "got:" + req.Msg}, nil
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	var got echoResp
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Echo != "got:" {
		t.Fatalf("echo=%q want got:", got.Echo)
	}
}

func TestHandler_Ratelimit_429(t *testing.T) {
	// 桶容量1,几乎不补:第一次放行,第二次429。
	tb := ratelimit.NewTokenBucket(1, 0.0001)
	defer tb.Stop()
	h := handler.New("", okHandler(),
		handler.WithRatelimit(tb, func(r *http.Request) string { return "u1" }),
	)
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: code=%d", rec1.Code)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second: code=%d want 429", rec2.Code)
	}
}
