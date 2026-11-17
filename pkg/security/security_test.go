package security_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rushteam/beauty/pkg/security"
)

func TestHashes(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"md5", security.MD5("abc"), "900150983cd24fb0d6963f7d28e17f72"},
		{"sha256", security.SHA256("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"sha512", security.SHA512("abc")[:16], "ddaf35a193617aba"},
		// RFC 4231 test case 2
		{"hs256", security.HS256("what do ya want for nothing?", "Jefe"), "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
		{"hs512", security.HS512("what do ya want for nothing?", "Jefe")[:16], "164b7a7bfcf819e2"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
	if !security.Equal("a", "a") || security.Equal("a", "b") || security.Equal("a", "ab") {
		t.Fatal("Equal 结果不对")
	}
}

func TestRandomString(t *testing.T) {
	if security.RandomString(0) != "" || security.PseudorandomString(-1) != "" {
		t.Fatal("n<=0 应返回空串")
	}
	s := security.RandomString(32)
	if len(s) != 32 {
		t.Fatalf("len = %d", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(security.DefaultAlphabet, r) {
			t.Fatalf("非法字符 %q", r)
		}
	}
	if security.RandomString(32) == s {
		t.Fatal("两次随机串相同")
	}
	u := security.RandomStringWithAlphabet(10, "甲乙")
	if utf8.RuneCountInString(u) != 10 || strings.Trim(u, "甲乙") != "" {
		t.Fatalf("unicode 字符集结果不对: %q", u)
	}
	p := security.PseudorandomStringWithAlphabet(8, "x")
	if p != "xxxxxxxx" {
		t.Fatalf("p = %q", p)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("空字符集应 panic")
		}
	}()
	security.RandomStringWithAlphabet(1, "")
}

func TestEncryptDecrypt(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	c1, err := security.Encrypt([]byte("hello"), key)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := security.Encrypt([]byte("hello"), key)
	if c1 == c2 {
		t.Fatal("随机 nonce 下两次密文不应相同")
	}
	plain, err := security.Decrypt(c1, key)
	if err != nil || string(plain) != "hello" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	if _, err := security.Decrypt(c1, []byte("fedcba9876543210fedcba9876543210")); !errors.Is(err, security.ErrDecrypt) {
		t.Fatalf("错误密钥应返回 ErrDecrypt, got %v", err)
	}
	tampered := []byte(c1)
	tampered[len(tampered)-3] ^= 1
	if _, err := security.Decrypt(string(tampered), key); err == nil {
		t.Fatal("篡改的密文应解密失败")
	}
	if _, err := security.Encrypt([]byte("x"), []byte("short")); err == nil {
		t.Fatal("非法密钥长度应报错")
	}
}

func TestPassword(t *testing.T) {
	h, err := security.HashPasswordWithCost("s3cret", 4)
	if err != nil {
		t.Fatal(err)
	}
	if !security.CheckPassword(h, "s3cret") || security.CheckPassword(h, "wrong") {
		t.Fatal("CheckPassword 结果不对")
	}
	if _, err := security.HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("超过 72 字节应报错")
	}
}

func TestJWT(t *testing.T) {
	secret := []byte("jwt-secret")
	tok, err := security.CreateJWT(map[string]any{"sub": "u1", "role": "admin"}, secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := security.ParseJWT(tok, secret)
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "u1" || claims["role"] != "admin" || claims["exp"] == nil {
		t.Fatalf("claims = %v", claims)
	}
	if _, err := security.ParseJWT(tok, []byte("other")); err == nil {
		t.Fatal("错误密钥应验签失败")
	}
	expired, _ := security.CreateJWT(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}, secret, 0)
	if _, err := security.ParseJWT(expired, secret); err == nil {
		t.Fatal("过期 token 应校验失败")
	}
	un, err := security.ParseUnverifiedJWT(tok)
	if err != nil || un["sub"] != "u1" {
		t.Fatalf("unverified = %v, %v", un, err)
	}
	// alg=none 必须被拒绝
	none := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJ1MSJ9."
	if _, err := security.ParseJWT(none, secret); err == nil {
		t.Fatal("alg=none 应被拒绝")
	}
}
