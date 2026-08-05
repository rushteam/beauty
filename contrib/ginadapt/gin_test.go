package ginadapt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type ctxKey struct{}

func injectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxKey{}, "injected-value")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func rejectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rejected", http.StatusForbidden)
	})
}

func TestWrap_PassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Wrap(injectMiddleware))
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Request.Context().Value(ctxKey{}).(string)
		c.String(http.StatusOK, v)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	if w.Body.String() != "injected-value" {
		t.Fatalf("context value not passed, got %q", w.Body.String())
	}
}

func TestWrap_Reject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	var handlerCalled bool
	r.Use(Wrap(rejectMiddleware))
	r.GET("/test", func(c *gin.Context) {
		handlerCalled = true
		c.String(http.StatusOK, "should not reach")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", w.Code)
	}
	if handlerCalled {
		t.Fatal("handler should not be called when middleware rejects")
	}
}
