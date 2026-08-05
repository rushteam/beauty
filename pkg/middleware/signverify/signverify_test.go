package signverify

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/auth"
)

var testSecret = []byte("test-secret-key")

func getSecret(appID string) ([]byte, bool) {
	if appID == "app1" {
		return testSecret, true
	}
	return nil, false
}

func ok200(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func signedRequest(method, path, body, userID string) *http.Request {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := Sign(testSecret, ts, userID, []byte(body))

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-App-Id", "app1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Sign", sig)
	req.Header.Set("X-User-Id", userID)
	return req
}

func TestMissingHeaders(t *testing.T) {
	h := HTTPMiddleware(getSecret)(http.HandlerFunc(ok200))
	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestValidSignature(t *testing.T) {
	h := HTTPMiddleware(getSecret)(http.HandlerFunc(ok200))
	req := signedRequest(http.MethodPost, "/api/pay", `{"amount":100}`, "user-42")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestInvalidSignature(t *testing.T) {
	h := HTTPMiddleware(getSecret)(http.HandlerFunc(ok200))
	req := signedRequest(http.MethodPost, "/api/pay", `{"amount":100}`, "user-42")
	req.Header.Set("X-Sign", "bad-signature")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestTimestampExpired(t *testing.T) {
	h := HTTPMiddleware(getSecret, WithMaxAge(time.Second))(http.HandlerFunc(ok200))

	ts := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	sig := Sign(testSecret, ts, "u1", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Header.Set("X-App-Id", "app1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Sign", sig)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestUnknownApp(t *testing.T) {
	h := HTTPMiddleware(getSecret)(http.HandlerFunc(ok200))
	req := signedRequest(http.MethodPost, "/api/pay", "", "")
	req.Header.Set("X-App-Id", "unknown")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestSkipPrefix(t *testing.T) {
	h := HTTPMiddleware(getSecret, WithSkipPrefixes("/healthz"))(http.HandlerFunc(ok200))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}

func TestExtractUser(t *testing.T) {
	var gotUser auth.User
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.GetUserFromContext(r.Context())
		if ok {
			gotUser = u
		}
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPMiddleware(getSecret, WithExtractUser())(handler)
	req := signedRequest(http.MethodPost, "/api/pay", `{"a":1}`, "user-99")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if gotUser == nil || gotUser.ID() != "user-99" {
		t.Fatalf("user not extracted, got %v", gotUser)
	}
}

func TestCustomHeaders(t *testing.T) {
	h := HTTPMiddleware(getSecret,
		WithAppIDHeader("App-Key"),
		WithSignHeader("Signature"),
		WithTimestampHeader("Request-Time"),
		WithUserIDHeader("Caller-Id"),
	)(http.HandlerFunc(ok200))

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"x":1}`
	sig := Sign(testSecret, ts, "c1", []byte(body))

	req := httptest.NewRequest(http.MethodPost, "/api/do", strings.NewReader(body))
	req.Header.Set("App-Key", "app1")
	req.Header.Set("Request-Time", ts)
	req.Header.Set("Signature", sig)
	req.Header.Set("Caller-Id", "c1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("custom headers: got %d, want 200", rec.Code)
	}
}

func TestSecretDeriver(t *testing.T) {
	// 模拟按请求时间戳派生 key：master + timestamp 前 8 位
	master := []byte("master-key")
	deriver := func(appID string, r *http.Request) ([]byte, bool) {
		if appID != "app1" {
			return nil, false
		}
		ts := r.Header.Get("X-Timestamp")
		if len(ts) < 8 {
			return nil, false
		}
		derived := append(master, []byte(ts[:8])...)
		return derived, true
	}

	h := HTTPMiddleware(nil, WithSecretDeriver(deriver))(http.HandlerFunc(ok200))

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	derivedKey := append(master, []byte(ts[:8])...)
	body := `{"data":1}`
	sig := Sign(derivedKey, ts, "u1", []byte(body))

	req := httptest.NewRequest(http.MethodPost, "/api/pay", strings.NewReader(body))
	req.Header.Set("X-App-Id", "app1")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Sign", sig)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("deriver: got %d, want 200", rec.Code)
	}
}

func TestSecretDeriverUnknownApp(t *testing.T) {
	deriver := func(appID string, r *http.Request) ([]byte, bool) {
		return nil, false
	}

	h := HTTPMiddleware(nil, WithSecretDeriver(deriver))(http.HandlerFunc(ok200))

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	req.Header.Set("X-App-Id", "unknown")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Sign", "anything")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("deriver unknown: got %d, want 401", rec.Code)
	}
}

func TestSignFunction(t *testing.T) {
	s1 := Sign(testSecret, "1700000000", "user1", []byte("body"))
	s2 := Sign(testSecret, "1700000000", "user1", []byte("body"))
	if s1 != s2 {
		t.Fatal("Sign should be deterministic")
	}

	s3 := Sign(testSecret, "1700000000", "user2", []byte("body"))
	if s1 == s3 {
		t.Fatal("different userID should produce different signature")
	}
}
