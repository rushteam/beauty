package recovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHTTPMiddleware_RecoversPanic(t *testing.T) {
	var panicVal any
	handler := HTTPMiddleware(WithOnPanic(func(_ context.Context, p any, _ []byte) {
		panicVal = p
	}))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if panicVal != "boom" {
		t.Fatalf("onPanic got %v, want boom", panicVal)
	}
}

func TestUnaryServerInterceptor_RecoversPanic(t *testing.T) {
	interceptor := UnaryServerInterceptor()
	handler := func(context.Context, any) (any, error) {
		panic("grpc panic")
	}

	_, err := interceptor(context.Background(), "req", &grpc.UnaryServerInfo{}, handler)
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
}

func TestHTTPMiddleware_NoPanic(t *testing.T) {
	handler := HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
