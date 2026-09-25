package sensitive

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/pkg/foundation/actrie"
)

// echoHandler 把请求体原样回写,用于验证脱敏后业务侧读到的内容。
func echoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	})
}

func doPost(t *testing.T, h http.Handler, ct, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestBlockMode(t *testing.T) {
	h := Block("敏感词", "违禁")(echoHandler())
	// 命中 → 403
	rr := doPost(t, h, "application/json", `{"text":"这是违禁内容"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("hit should be 403, got %d", rr.Code)
	}
	// 未命中 → 200 且原样透传
	rr = doPost(t, h, "application/json", `{"text":"正常内容"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("clean should be 200, got %d", rr.Code)
	}
	if rr.Body.String() != `{"text":"正常内容"}` {
		t.Fatalf("clean body altered: %q", rr.Body.String())
	}
}

func TestMaskMode(t *testing.T) {
	h := Mask("敏感词", "违禁")(echoHandler())
	rr := doPost(t, h, "application/json", `{"text":"含敏感词和违禁"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("mask should pass through 200, got %d", rr.Code)
	}
	got := rr.Body.String()
	if strings.Contains(got, "敏感词") || strings.Contains(got, "违禁") {
		t.Fatalf("masked body still leaks: %q", got)
	}
	if want := `{"text":"含***和**"}`; got != want {
		t.Fatalf("masked=%q want %q", got, want)
	}
}

func TestNonTextContentTypePassThrough(t *testing.T) {
	h := Block("违禁")(echoHandler())
	// 二进制类型不扫描,即便含"违禁"也放行
	rr := doPost(t, h, "application/octet-stream", "违禁")
	if rr.Code != http.StatusOK {
		t.Fatalf("binary should pass, got %d", rr.Code)
	}
}

func TestSkipPaths(t *testing.T) {
	m := actrie.New()
	m.AddAll("违禁")
	h := Middleware(Config{Matcher: m, Mode: ModeBlock, SkipPaths: []string{"/health"}})(echoHandler())
	req := httptest.NewRequest(http.MethodPost, "/health", strings.NewReader(`违禁`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("skip path should pass, got %d", rr.Code)
	}
}

func TestScanQueryBlock(t *testing.T) {
	m := actrie.New()
	m.AddAll("违禁")
	h := Middleware(Config{Matcher: m, Mode: ModeBlock, ScanQuery: true})(echoHandler())
	req := httptest.NewRequest(http.MethodGet, "/search?q="+urlEnc("违禁词"), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("query hit should be 403, got %d", rr.Code)
	}
}

func TestNormalizerEvasion(t *testing.T) {
	// 反绕过:剥离非字母 + 大小写折叠,命中 "f*u*c*k"
	norm := actrie.Chain(actrie.Keep(func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }), actrie.FoldCase())
	m := actrie.New(actrie.WithNormalizer(norm))
	m.Add("fuck")
	h := Middleware(Config{Matcher: m, Mode: ModeBlock})(echoHandler())
	rr := doPost(t, h, "text/plain", "you f*u*c*k")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("evasion should be blocked, got %d", rr.Code)
	}
}

func TestNilMatcherPassThrough(t *testing.T) {
	h := Middleware(Config{})(echoHandler())
	rr := doPost(t, h, "text/plain", "anything")
	if rr.Code != http.StatusOK {
		t.Fatalf("nil matcher should pass through, got %d", rr.Code)
	}
}

func TestCustomOnBlocked(t *testing.T) {
	m := actrie.New()
	m.AddAll("违禁")
	var capturedHit string
	h := Middleware(Config{
		Matcher: m, Mode: ModeBlock,
		OnBlocked: func(w http.ResponseWriter, r *http.Request, hit string) {
			capturedHit = hit
			w.WriteHeader(http.StatusUnavailableForLegalReasons)
		},
	})(echoHandler())
	rr := doPost(t, h, "text/plain", "含违禁词")
	if rr.Code != http.StatusUnavailableForLegalReasons {
		t.Fatalf("custom status got %d", rr.Code)
	}
	if capturedHit != "违禁" {
		t.Fatalf("captured hit=%q want 违禁", capturedHit)
	}
}

func urlEnc(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	return b.String()
}
