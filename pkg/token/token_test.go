package token_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/token"
)

func newMgr(t *testing.T) *token.Manager {
	t.Helper()
	return token.New(
		token.WithSessionKey([]byte("sess-secret-32-bytes-aaaaaaaaaa")),
		token.WithRefreshKey([]byte("refresh-secret-different-32b")),
		token.WithSessionTTL(time.Hour),
		token.WithRefreshTTL(24*time.Hour),
	)
}

func TestToken_IssueAndVerify(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	sess, refresh, err := m.Issue("u1", "alice", map[string]string{"role": "admin"}, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if sess == "" || refresh == "" || sess == refresh {
		t.Fatal("bad tokens")
	}
	c, err := m.Verify(sess)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.UserID != "u1" || c.Username != "alice" || c.Vars["role"] != "admin" {
		t.Fatalf("claims=%+v", c)
	}
	if c.TokenID == "" {
		t.Fatal("token id empty")
	}
}

func TestToken_DualKeySeparation(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	sess, _, _ := m.Issue("u1", "", nil, "")
	// session token 用 refresh key 验证应失败(密钥分离)。
	if _, err := m.VerifyRefresh(sess); !errors.Is(err, token.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestToken_Refresh(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	_, refresh, _ := m.Issue("u1", "alice", map[string]string{"k": "v"}, "tid-1")
	newSess, err := m.Refresh(refresh, nil)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	c, err := m.Verify(newSess)
	if err != nil {
		t.Fatalf("verify refreshed: %v", err)
	}
	if c.UserID != "u1" || c.Vars["k"] != "v" {
		t.Fatalf("claims lost: %+v", c)
	}
	// 复用原 tokenID。
	if c.TokenID != "tid-1" {
		t.Fatalf("tokenID changed: %s", c.TokenID)
	}
}

func TestToken_RefreshWithNewVars(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	_, refresh, _ := m.Issue("u1", "", map[string]string{"old": "1"}, "tid")
	newVars := map[string]string{"new": "2"}
	newSess, _ := m.Refresh(refresh, &newVars)
	c, _ := m.Verify(newSess)
	if c.Vars["new"] != "2" || c.Vars["old"] != "" {
		t.Fatalf("vars not replaced: %+v", c.Vars)
	}
}

func TestToken_RevokeSingle(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	sess, refresh, _ := m.Issue("u1", "", nil, "tid-x")
	c, _ := m.Verify(sess)
	m.Revoke(c.TokenID)
	if _, err := m.Verify(sess); !errors.Is(err, token.ErrRevoked) {
		t.Fatalf("session want ErrRevoked, got %v", err)
	}
	if _, err := m.Refresh(refresh, nil); !errors.Is(err, token.ErrRevoked) {
		t.Fatalf("refresh want ErrRevoked, got %v", err)
	}
}

func TestToken_RevokeAll(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	sess1, _, _ := m.Issue("u1", "", nil, "tid-1")
	m.RevokeAll("u1")
	if _, err := m.Verify(sess1); !errors.Is(err, token.ErrKicked) {
		t.Fatalf("want ErrKicked, got %v", err)
	}
	// RevokeAll 后等过下一秒,新签发的 token(iat > kickedAt)应可用。
	// jwt iat 截断到秒,需跨过整秒边界。
	time.Sleep(1100 * time.Millisecond)
	sess2, _, _ := m.Issue("u1", "", nil, "tid-2")
	if _, err := m.Verify(sess2); err != nil {
		t.Fatalf("new token after kick should work: %v", err)
	}
}

func TestToken_Expired(t *testing.T) {
	m := token.New(
		token.WithSessionKey([]byte("k")),
		token.WithRefreshKey([]byte("r")),
		token.WithSessionTTL(-time.Second), // 已过期
		token.WithRefreshTTL(time.Hour),
	)
	defer m.Stop()
	sess, _, _ := m.Issue("u1", "", nil, "")
	if _, err := m.Verify(sess); !errors.Is(err, token.ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestToken_TamperedSignature(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	sess, _, _ := m.Issue("u1", "", nil, "")
	// 篡改签名段最后一字符(若为 A 则改 B,否则改 A)。
	sig := sess[strings.LastIndexByte(sess, '.')+1:]
	b := []byte(sig)
	last := b[len(b)-1]
	if last == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	tampered := sess[:len(sess)-len(sig)] + string(b)
	if _, err := m.Verify(tampered); !errors.Is(err, token.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestToken_TamperedPayload(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	sess, _, _ := m.Issue("u1", "", nil, "")
	// 篡改 payload 段(中间段最后一字符翻转)。
	parts := strings.SplitN(sess, ".", 3)
	b := []byte(parts[1])
	last := b[len(b)-1]
	if last == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	parts[1] = string(b)
	tampered := strings.Join(parts, ".")
	if _, err := m.Verify(tampered); !errors.Is(err, token.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestToken_KeyMismatch(t *testing.T) {
	m1 := token.New(token.WithSessionKey([]byte("key-1")))
	m2 := token.New(token.WithSessionKey([]byte("key-2")))
	defer m1.Stop()
	defer m2.Stop()
	sess, _, _ := m1.Issue("u1", "", nil, "")
	if _, err := m2.Verify(sess); !errors.Is(err, token.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken for key mismatch, got %v", err)
	}
}

func TestToken_ConcurrentIssue(t *testing.T) {
	m := newMgr(t)
	defer m.Stop()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			sess, _, err := m.Issue("u1", "", map[string]string{"x": "y"}, "")
			if err != nil {
				t.Errorf("issue: %v", err)
				return
			}
			if _, err := m.Verify(sess); err != nil {
				t.Errorf("verify: %v", err)
			}
		})
	}
	wg.Wait()
}
