package bedrock

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// AWS 官方 SigV4 测试向量:get-vanilla 用例(header 签名,SignedHeaders=host;x-amz-date)。
// 见 https://docs.aws.amazon.com/general/latest/gr/sigv4-signed-request-examples.html 及
// aws4_testsuite。凭据/时间/期望签名均为公开固定向量。
func TestSignV4_GetVanilla(t *testing.T) {
	creds := Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

	signV4(req, nil, creds, "service", "us-east-1", tm)

	want := "AWS4-HMAC-SHA256 " +
		"Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, " +
		"Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Fatalf("X-Amz-Date = %q", got)
	}
}

// AWS 文档 "derive a signing key" 示例:secret + 20150830 / us-east-1 / iam。
func TestDeriveSigningKey(t *testing.T) {
	key := deriveSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20150830", "us-east-1", "iam")
	want := "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	if got := hex.EncodeToString(key); got != want {
		t.Fatalf("signing key = %s want %s", got, want)
	}
}

func TestSignV4_SessionTokenHeader(t *testing.T) {
	creds := Credentials{AccessKeyID: "AK", SecretAccessKey: "sk", SessionToken: "tok123"}
	req, _ := http.NewRequest(http.MethodPost, "http://x.amazonaws.com/foo", nil)
	signV4(req, []byte(`{}`), creds, "bedrock", "us-east-1", time.Unix(0, 0))
	if req.Header.Get("X-Amz-Security-Token") != "tok123" {
		t.Fatalf("session token header not set")
	}
	// session token 必须进 SignedHeaders
	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "SignedHeaders=host;x-amz-date;x-amz-security-token") {
		t.Fatalf("session token not in signed headers: %s", auth)
	}
}

func TestUriEncode(t *testing.T) {
	if got := uriEncode("a b/c:d", true); got != "a%20b%2Fc%3Ad" {
		t.Fatalf("encodeSlash=true: %s", got)
	}
	if got := uriEncode("a/b", false); got != "a/b" {
		t.Fatalf("encodeSlash=false should keep '/': %s", got)
	}
	if got := uriEncode("Aa0-._~", true); got != "Aa0-._~" {
		t.Fatalf("unreserved should pass through: %s", got)
	}
}

func TestTrimAll(t *testing.T) {
	if got := trimAll("  a   b\tc  "); got != "a b c" {
		t.Fatalf("trimAll folded wrong: %q", got)
	}
}
