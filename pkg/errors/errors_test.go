package errors_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/errors"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// ---- Code 注册与映射 -----

func TestCode_Predefined_HTTPMapping(t *testing.T) {
	cases := []struct {
		code     errors.Code
		wantHTTP int
		wantGRPC uint32
	}{
		{errors.CodeInvalidArgument, 400, 3},
		{errors.CodeUnauthenticated, 401, 16},
		{errors.CodeForbidden, 403, 7},
		{errors.CodeNotFound, 404, 5},
		{errors.CodeConflict, 409, 6},
		{errors.CodeTooManyRequests, 429, 8},
		{errors.CodeInternal, 500, 13},
		{errors.CodeUnavailable, 503, 14},
	}
	for _, tc := range cases {
		if got := tc.code.HTTPStatus(); got != tc.wantHTTP {
			t.Errorf("code=%d HTTPStatus: want %d got %d", tc.code, tc.wantHTTP, got)
		}
		if got := tc.code.GRPCCode(); got != tc.wantGRPC {
			t.Errorf("code=%d GRPCCode: want %d got %d", tc.code, tc.wantGRPC, got)
		}
	}
}

func TestCode_UnregisteredFallback(t *testing.T) {
	unknown := errors.Code(99999)
	if got := unknown.HTTPStatus(); got != 500 {
		t.Errorf("unregistered code: HTTPStatus want 500 got %d", got)
	}
	if got := unknown.GRPCCode(); got != 13 {
		t.Errorf("unregistered code: GRPCCode want 13 (Internal) got %d", got)
	}
}

func TestRegister_Duplicate_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	errors.Register(errors.Code(88888), 400, 3, "first")
	errors.Register(errors.Code(88888), 400, 3, "second") // must panic
}

// ---- Status 构造与接口 ----

func TestStatus_Error(t *testing.T) {
	s := errors.New(errors.CodeNotFound, "user not found")
	if !strings.Contains(s.Error(), "user not found") {
		t.Errorf("Error() should contain message: %s", s.Error())
	}
	if !strings.Contains(s.Error(), "404") {
		t.Errorf("Error() should contain code: %s", s.Error())
	}
}

func TestStatus_WithCause(t *testing.T) {
	cause := fmt.Errorf("db connection refused")
	s := errors.New(errors.CodeInternal, "").WithCause(cause)
	if s.Cause() != cause {
		t.Error("Cause() should return the wrapped error")
	}
	// Error() 包含 cause
	if !strings.Contains(s.Error(), "db connection refused") {
		t.Errorf("Error() should include cause: %s", s.Error())
	}
}

func TestStatus_Is(t *testing.T) {
	err := errors.NotFound("user 123 not found")

	// errors.Is 按 code 匹配
	if !isCode(err, errors.CodeNotFound) {
		t.Error("errors.Is should match by code")
	}
	if isCode(err, errors.CodeInternal) {
		t.Error("errors.Is should not match different code")
	}
}

func TestStatus_Unwrap(t *testing.T) {
	cause := fmt.Errorf("original")
	s := errors.New(errors.CodeInternal, "wrapped").WithCause(cause)
	// errors.Unwrap 能穿透到 cause
	if s.Unwrap() != cause {
		t.Error("Unwrap should return cause")
	}
}

func TestStatus_Details(t *testing.T) {
	s := errors.InvalidArgument("validation failed").
		WithDetail(&errors.FieldViolation{Field: "email", Description: "invalid format"}).
		WithDetail(&errors.FieldViolation{Field: "name", Description: "required"})

	if len(s.Details()) != 2 {
		t.Errorf("want 2 details, got %d", len(s.Details()))
	}
}

func TestFromError(t *testing.T) {
	// 直接是 *Status
	s, ok := errors.FromError(errors.NotFound("x"))
	if !ok || s.Code() != errors.CodeNotFound {
		t.Error("FromError should extract *Status directly")
	}

	// wrapped *Status
	wrapped := fmt.Errorf("outer: %w", errors.NotFound("x"))
	s, ok = errors.FromError(wrapped)
	if !ok || s.Code() != errors.CodeNotFound {
		t.Error("FromError should extract *Status from wrapped error")
	}

	// 普通 error
	_, ok = errors.FromError(fmt.Errorf("plain error"))
	if ok {
		t.Error("FromError should return false for plain error")
	}

	// nil
	_, ok = errors.FromError(nil)
	if ok {
		t.Error("FromError should return false for nil")
	}
}

// ---- HTTP 序列化 ----

func TestWriteHTTP_StatusCode(t *testing.T) {
	cases := []struct {
		s        *errors.Status
		wantCode int
	}{
		{errors.NotFound("x"), 404},
		{errors.InvalidArgument("x"), 400},
		{errors.Internal("x"), 500},
		{errors.Unauthenticated("x"), 401},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		errors.WriteHTTP(w, tc.s)
		if w.Code != tc.wantCode {
			t.Errorf("code=%d: HTTP status want %d got %d", tc.s.Code(), tc.wantCode, w.Code)
		}
	}
}

func TestWriteHTTP_JSONBody(t *testing.T) {
	s := errors.InvalidArgument("validation failed").
		WithDetail(&errors.FieldViolation{Field: "email", Description: "invalid"})

	w := httptest.NewRecorder()
	errors.WriteHTTP(w, s)

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp["code"] != float64(400) {
		t.Errorf("JSON code: want 400 got %v", resp["code"])
	}
	if resp["message"] != "validation failed" {
		t.Errorf("JSON message: want 'validation failed' got %v", resp["message"])
	}
	details, ok := resp["details"].([]any)
	if !ok || len(details) == 0 {
		t.Error("JSON details should be non-empty array")
	}
}

func TestWriteHTTP_NoCauseInResponse(t *testing.T) {
	cause := fmt.Errorf("secret db password in error")
	s := errors.Internal("something went wrong").WithCause(cause)

	w := httptest.NewRecorder()
	errors.WriteHTTP(w, s)

	body := w.Body.String()
	if strings.Contains(body, "secret db password") {
		t.Error("cause must NOT appear in HTTP response body")
	}
}

// ---- gRPC 转换 ----

func TestToGRPC(t *testing.T) {
	grpcErr := errors.ToGRPC(errors.NotFound("user not found"))
	st, ok := grpcstatus.FromError(grpcErr)
	if !ok {
		t.Fatal("ToGRPC should produce a gRPC status error")
	}
	if st.Code() != codes.NotFound {
		t.Errorf("gRPC code: want NotFound got %v", st.Code())
	}
	if st.Message() != "user not found" {
		t.Errorf("gRPC message: want 'user not found' got %q", st.Message())
	}
}

func TestFromGRPCError(t *testing.T) {
	grpcErr := grpcstatus.Error(codes.NotFound, "item missing")
	s, ok := errors.FromGRPCError(grpcErr)
	if !ok {
		t.Fatal("FromGRPCError should succeed for gRPC status error")
	}
	if s.Code() != errors.CodeNotFound {
		t.Errorf("Code: want CodeNotFound got %v", s.Code())
	}
	if s.Message() != "item missing" {
		t.Errorf("Message: want 'item missing' got %q", s.Message())
	}
}

// ---- HTTPMiddlewareErrorHandler + SetError ----

func TestHTTPMiddlewareErrorHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errors.SetError(r.Context(), errors.NotFound("order not found"))
	})
	wrapped := errors.HTTPMiddlewareErrorHandler(handler)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Errorf("want 404 got %d", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["message"] != "order not found" {
		t.Errorf("message: want 'order not found' got %v", resp["message"])
	}
}

// ---- Details ----

func TestDetails_RetryInfo(t *testing.T) {
	s := errors.TooManyRequests("rate limit exceeded").
		WithDetail(&errors.RetryInfo{RetryDelay: 5 * time.Second})

	w := httptest.NewRecorder()
	errors.WriteHTTP(w, s)

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	details := resp["details"].([]any)
	d := details[0].(map[string]any)
	if d["type"] != "RetryInfo" {
		t.Errorf("detail type: want RetryInfo got %v", d["type"])
	}
}

// ---- helper ----

func isCode(err error, code errors.Code) bool {
	target := errors.New(code, "")
	return isErr(err, target)
}

func isErr(err, target error) bool {
	// 手动实现 errors.Is 逻辑，兼容测试包内调用
	for err != nil {
		if err == target {
			return true
		}
		if x, ok := err.(interface{ Is(error) bool }); ok && x.Is(target) {
			return true
		}
		err = unwrap(err)
	}
	return false
}

func unwrap(err error) error {
	if u, ok := err.(interface{ Unwrap() error }); ok {
		return u.Unwrap()
	}
	return nil
}
