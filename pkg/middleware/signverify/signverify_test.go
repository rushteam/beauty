package signverify

import (
	"crypto/hmac"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/store/kvstore"
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

func TestDerivedKey(t *testing.T) {
	h := HTTPMiddleware(getSecret, WithDerivedKey(300))(http.HandlerFunc(ok200))

	ts := time.Now().Unix()
	tsStr := strconv.FormatInt(ts, 10)
	dk := DeriveKey(testSecret, ts, 300)
	body := `{"amount":50}`
	sig := Sign(dk, tsStr, "user-1", []byte(body))

	req := httptest.NewRequest(http.MethodPost, "/api/pay", strings.NewReader(body))
	req.Header.Set("X-App-Id", "app1")
	req.Header.Set("X-Timestamp", tsStr)
	req.Header.Set("X-Sign", sig)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("derived key: got %d, want 200", rec.Code)
	}
}

func TestDerivedKeyPreviousWindow(t *testing.T) {
	h := HTTPMiddleware(getSecret, WithDerivedKey(300), WithMaxAge(10*time.Minute))(http.HandlerFunc(ok200))

	// 用上一个窗口的 derivedKey 签名（模拟窗口边界漂移）
	ts := time.Now().Unix()
	tsStr := strconv.FormatInt(ts, 10)
	dk := DeriveKey(testSecret, ts-300, 300) // 上一个窗口
	body := `{"x":1}`
	sig := Sign(dk, tsStr, "u2", []byte(body))

	req := httptest.NewRequest(http.MethodPost, "/api/do", strings.NewReader(body))
	req.Header.Set("X-App-Id", "app1")
	req.Header.Set("X-Timestamp", tsStr)
	req.Header.Set("X-Sign", sig)
	req.Header.Set("X-User-Id", "u2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("previous window: got %d, want 200", rec.Code)
	}
}

func TestDerivedKeyWrongMaster(t *testing.T) {
	h := HTTPMiddleware(getSecret, WithDerivedKey(300))(http.HandlerFunc(ok200))

	ts := time.Now().Unix()
	tsStr := strconv.FormatInt(ts, 10)
	dk := DeriveKey([]byte("wrong-master"), ts, 300)
	sig := Sign(dk, tsStr, "u1", nil)

	req := httptest.NewRequest(http.MethodPost, "/api/pay", nil)
	req.Header.Set("X-App-Id", "app1")
	req.Header.Set("X-Timestamp", tsStr)
	req.Header.Set("X-Sign", sig)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong master: got %d, want 401", rec.Code)
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	master := []byte("key")
	// 1700000000 / 300 = 5666666, 1700000050 / 300 = 5666666 (同窗口)
	dk1 := DeriveKey(master, 1700000000, 300)
	dk2 := DeriveKey(master, 1700000050, 300)
	// 1700000400 / 300 = 5666668 (不同窗口)
	dk3 := DeriveKey(master, 1700000400, 300)

	if !hmac.Equal(dk1, dk2) {
		t.Fatal("same window should produce same derived key")
	}
	if hmac.Equal(dk1, dk3) {
		t.Fatal("different window should produce different derived key")
	}
}

func TestDirectMode(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()

	var gotUser auth.User
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.GetUserFromContext(r.Context())
		if ok {
			gotUser = u
		}
		w.WriteHeader(http.StatusOK)
	})

	h := DirectMode(store, getSecret, WithSkipPrefixes("/healthz"))(handler)

	// 正常请求
	req := signedRequest(http.MethodPost, "/api/pay", `{"a":1}`, "user-7")
	req.Header.Set("X-Nonce", "full-chain-nonce-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("full chain: got %d, want 200", rec.Code)
	}
	if gotUser == nil || gotUser.ID() != "user-7" {
		t.Fatalf("user not extracted, got %v", gotUser)
	}

	// 重放同一 nonce → 403
	req2 := signedRequest(http.MethodPost, "/api/pay", `{"a":1}`, "user-7")
	req2.Header.Set("X-Nonce", "full-chain-nonce-1")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusForbidden {
		t.Fatalf("replay: got %d, want 403", rec2.Code)
	}

	// skip prefix
	req3 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("skip: got %d, want 200", rec3.Code)
	}
}

func TestBehindGateway(t *testing.T) {
	var gotUser auth.User
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.GetUserFromContext(r.Context())
		if ok {
			gotUser = u
		}
		w.WriteHeader(http.StatusOK)
	})

	h := BehindGateway(getSecret)(handler)

	req := signedRequest(http.MethodPost, "/api/order", `{"id":1}`, "user-88")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("gateway mode: got %d, want 200", rec.Code)
	}
	if gotUser == nil || gotUser.ID() != "user-88" {
		t.Fatalf("user not extracted, got %v", gotUser)
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
