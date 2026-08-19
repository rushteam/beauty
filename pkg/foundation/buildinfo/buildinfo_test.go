package buildinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// 默认(无 ldflags 注入):GoVersion 必有;Version 至少兜底 unknown 或来自 VCS/模块。
func TestGet_Defaults(t *testing.T) {
	info := Get()
	if info.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.Version == "" {
		t.Fatal("Version 不应为空(至少兜底 unknown)")
	}
}

// ldflags 注入优先:设置包级变量后 Get 应采用之。
func TestGet_LdflagsOverride(t *testing.T) {
	oldV, oldC, oldT := version, commit, buildTime
	t.Cleanup(func() { version, commit, buildTime = oldV, oldC, oldT })

	version = "9.9.9"
	commit = "abcdef1234567890"
	buildTime = "2026-01-02T03:04:05Z"

	info := Get()
	if info.Version != "9.9.9" {
		t.Fatalf("Version = %q, want 9.9.9", info.Version)
	}
	if info.Commit != "abcdef1234567890" {
		t.Fatalf("Commit = %q", info.Commit)
	}
	if info.Short() != "abcdef123456" {
		t.Fatalf("Short = %q, want abcdef123456", info.Short())
	}
	if !strings.Contains(info.String(), "version=9.9.9") ||
		!strings.Contains(info.String(), "commit=abcdef123456") ||
		!strings.Contains(info.String(), "built=2026-01-02T03:04:05Z") {
		t.Fatalf("String 摘要不含预期字段: %q", info.String())
	}
}

// Handler 输出合法 JSON,含 version 字段。
func TestHandler(t *testing.T) {
	oldV := version
	t.Cleanup(func() { version = oldV })
	version = "1.0.0"

	rec := httptest.NewRecorder()
	Handler()(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got Info
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Fatalf("json version = %q, want 1.0.0", got.Version)
	}
}
