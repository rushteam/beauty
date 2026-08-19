package accesslog

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/pkg/middleware/requestid"
)

func TestHTTPMiddleware_LogsAccess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	old := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(old) })

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/items", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req = req.WithContext(requestid.NewContext(req.Context(), "req-42"))
	handler.ServeHTTP(rec, req)

	out := buf.String()
	if !strings.Contains(out, "access") {
		t.Fatalf("log missing access record: %q", out)
	}
	if !strings.Contains(out, "req-42") {
		t.Fatalf("log missing request_id: %q", out)
	}
	if !strings.Contains(out, "/api/items") {
		t.Fatalf("log missing path: %q", out)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
}
