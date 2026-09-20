package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	perr "github.com/rushteam/beauty/pkg/api/errors"
	"github.com/rushteam/beauty/pkg/api/handler"
)

func TestAsyncHandler_SyncRespond(t *testing.T) {
	// 在同一 goroutine 直接调 respond(同步模式)。
	h := handler.NewAsync("", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		respond(&echoResp{Echo: req.Msg}, nil)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"async"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var got echoResp
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Echo != "async" {
		t.Fatalf("echo=%q", got.Echo)
	}
}

func TestAsyncHandler_GoRoutineRespond(t *testing.T) {
	// 在另一 goroutine 异步调 respond。
	h := handler.NewAsync("", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		go func() {
			time.Sleep(10 * time.Millisecond)
			respond(&echoResp{Echo: "delayed:" + req.Msg}, nil)
		}()
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"Msg":"go"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	var got echoResp
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Echo != "delayed:go" {
		t.Fatalf("echo=%q", got.Echo)
	}
}

func TestAsyncHandler_Error(t *testing.T) {
	h := handler.NewAsync("", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		respond(nil, perr.New(perr.CodeForbidden, "denied"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
}

func TestAsyncHandler_NilResp_204(t *testing.T) {
	h := handler.NewAsync("", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		respond(nil, nil)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204", rec.Code)
	}
}

func TestAsyncHandler_RespondOnce(t *testing.T) {
	// respond 只能调一次,第二次返回 false。
	h := handler.NewAsync("", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		ok1 := respond(&echoResp{Echo: "first"}, nil)
		ok2 := respond(&echoResp{Echo: "second"}, nil)
		if !ok1 {
			t.Error("第一次 respond 应返回 true")
		}
		if ok2 {
			t.Error("第二次 respond 应返回 false")
		}
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestAsyncHandler_MethodNotAllowed(t *testing.T) {
	h := handler.NewAsync("POST", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		respond(&echoResp{}, nil)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code=%d want 501", rec.Code)
	}
}

func TestAsyncHandler_WithMiddleware(t *testing.T) {
	var seq []string
	h := handler.NewAsync("", func(ctx context.Context, req *echoReq, respond handler.Respond[echoResp]) {
		respond(&echoResp{Echo: "ok"}, nil)
	}, handler.WithMiddleware(tagMW("mw", &seq, false)))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if len(seq) != 1 || seq[0] != "mw" {
		t.Fatalf("中间件未执行, seq=%v", seq)
	}
}
