package auth

import (
	"context"
	"testing"
	"time"
)

func TestSimpleJWT_ValidAndTampered(t *testing.T) {
	a := NewSimpleJWTAuthenticator("s3cr3t")
	tok, err := a.CreateToken("u1", "alice", []string{"admin"}, time.Hour)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// 合法 token 应通过
	u, err := a.Authenticate(context.Background(), tok)
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if u.ID() != "u1" {
		t.Fatalf("want user u1, got %s", u.ID())
	}

	// 篡改签名最后一字节应被拒绝（验证常量时间比较仍正确判负）
	b := []byte(tok)
	b[len(b)-1] ^= 0x01
	if _, err := a.Authenticate(context.Background(), string(b)); err == nil {
		t.Fatal("tampered signature must be rejected")
	}
}
