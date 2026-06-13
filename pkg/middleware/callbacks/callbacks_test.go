package callbacks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	cb "github.com/rushteam/beauty/pkg/callbacks"
	"google.golang.org/grpc"
)

// 收集触发的时机，便于断言。
type recorder struct {
	timings []string
}

func (r *recorder) handler() cb.Handler {
	return cb.NewHandlerBuilder().
		OnStart(func(ctx context.Context, _ *cb.RunInfo, _ any) context.Context {
			r.timings = append(r.timings, "start")
			return ctx
		}).
		OnEnd(func(ctx context.Context, _ *cb.RunInfo, _ any) context.Context {
			r.timings = append(r.timings, "end")
			return ctx
		}).
		OnError(func(ctx context.Context, _ *cb.RunInfo, _ error) context.Context {
			r.timings = append(r.timings, "error")
			return ctx
		}).
		Build()
}

func TestHTTPMiddleware_Success(t *testing.T) {
	rec := &recorder{}
	h := HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx := cb.WithHandlers(context.Background(), rec.handler())
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(rec.timings) != 2 || rec.timings[0] != "start" || rec.timings[1] != "end" {
		t.Fatalf("want [start end], got %v", rec.timings)
	}
}

func TestHTTPMiddleware_5xxIsError(t *testing.T) {
	rec := &recorder{}
	h := HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	ctx := cb.WithHandlers(context.Background(), rec.handler())
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(rec.timings) != 2 || rec.timings[1] != "error" {
		t.Fatalf("5xx should fire error, got %v", rec.timings)
	}
}

// statusWriter 应透传 Flusher（经 ResponseController）。
func TestStatusWriter_FlushPassThrough(t *testing.T) {
	flushed := false
	h := HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); err == nil {
			flushed = true
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !flushed {
		t.Fatal("Flush should pass through statusWriter via Unwrap")
	}
}

func TestUnaryServerInterceptor(t *testing.T) {
	rec := &recorder{}
	ictor := UnaryServerInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Method"}

	// 成功
	ctx := cb.WithHandlers(context.Background(), rec.handler())
	_, err := ictor(ctx, "req", info, func(ctx context.Context, req any) (any, error) {
		return "resp", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.timings) != 2 || rec.timings[1] != "end" {
		t.Fatalf("want [start end], got %v", rec.timings)
	}

	// 失败
	rec2 := &recorder{}
	ctx2 := cb.WithHandlers(context.Background(), rec2.handler())
	_, _ = ictor(ctx2, "req", info, func(ctx context.Context, req any) (any, error) {
		return nil, errors.New("boom")
	})
	if len(rec2.timings) != 2 || rec2.timings[1] != "error" {
		t.Fatalf("error path should fire error, got %v", rec2.timings)
	}
}
