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
	"time"

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

// tagMW 记录进出顺序并可选短路,用于验证中间件挂载与顺序。
func tagMW(name string, log *[]string, block bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*log = append(*log, name)
			if block {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WithMiddleware:挂上的中间件应执行,放行则到达业务函数。
func TestHandler_WithMiddleware_Passthrough(t *testing.T) {
	var seq []string
	h := handler.New("", okHandler(), handler.WithMiddleware(tagMW("mw", &seq, false)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"hi"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("放行应到业务函数, code=%d", rec.Code)
	}
	if len(seq) != 1 || seq[0] != "mw" {
		t.Fatalf("中间件应被执行, seq=%v", seq)
	}
}

// 中间件可提前短路,业务函数不执行。
func TestHandler_WithMiddleware_ShortCircuit(t *testing.T) {
	var reached atomic.Bool
	fn := func(ctx context.Context, req *echoReq) (*echoResp, error) {
		reached.Store(true)
		return &echoResp{}, nil
	}
	var seq []string
	h := handler.New("", handler.Func[echoReq, echoResp](fn), handler.WithMiddleware(tagMW("guard", &seq, true)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"x"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("短路应返回 403, code=%d", rec.Code)
	}
	if reached.Load() {
		t.Fatal("短路后业务函数不应执行")
	}
}

// 多个中间件:靠前者在更外层(先执行)。
func TestHandler_WithMiddleware_Order(t *testing.T) {
	var seq []string
	h := handler.New("", okHandler(),
		handler.WithMiddleware(tagMW("a", &seq, false), tagMW("b", &seq, false)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"hi"}`)))
	if len(seq) != 2 || seq[0] != "a" || seq[1] != "b" {
		t.Fatalf("靠前者应在更外层先执行, seq=%v", seq)
	}
}

// 用户中间件在 ratelimit 之外:即使请求被限流拦截(429),外层中间件仍会执行。
func TestHandler_WithMiddleware_OutermostBeforeRatelimit(t *testing.T) {
	var seq []string
	lim := ratelimit.NewSlidingWindow(1, time.Hour) // 窗口内只允许 1 次
	defer lim.Stop()
	h := handler.New("", okHandler(),
		handler.WithMiddleware(tagMW("outer", &seq, false)),
		handler.WithRatelimit(lim, func(*http.Request) string { return "k" }),
	)
	req := func() *http.Request { return httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"hi"}`)) }

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req()) // 第 1 次:放行
	if rec1.Code != http.StatusOK {
		t.Fatalf("第 1 次应放行, code=%d", rec1.Code)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req()) // 第 2 次:被限流 429
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("第 2 次应被限流 429, code=%d", rec2.Code)
	}
	// 两次请求外层中间件都执行了(证明它在 ratelimit 之外)。
	if len(seq) != 2 {
		t.Fatalf("外层中间件应在限流之外、两次都执行, seq=%v", seq)
	}
}
