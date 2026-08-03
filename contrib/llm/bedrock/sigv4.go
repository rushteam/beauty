package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4 签名(纯标准库实现,不引 AWS SDK)。
// 参考:https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv4-signing-elements.html
//
// 只实现 Bedrock 需要用到的部分:对 POST /model/{id}/invoke 这类请求,用 Authorization 头做
// header 签名(非预签名 URL)。签名头集合固定为 host;x-amz-date(有 session token 时加
// x-amz-security-token)。payload 以 sha256 摘要参与 canonical request。

const (
	sigv4Algorithm = "AWS4-HMAC-SHA256"
	sigv4Request   = "aws4_request"
)

// Credentials 是一组 AWS 凭据。SessionToken 仅在使用临时凭据(STS/AssumeRole)时非空。
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// signV4 对 req 做 SigV4 header 签名,原地设置 X-Amz-Date、(有则)X-Amz-Security-Token
// 与 Authorization 头。payload 是完整请求体(用于摘要),service 如 "bedrock",region 如 "us-east-1"。
func signV4(req *http.Request, payload []byte, creds Credentials, service, region string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	payloadHash := hexSHA256(payload)

	// 1) canonical request
	canonicalHeaders, signedHeaders := canonicalHeaders(req)
	canonicalReq := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		canonicalQuery(req),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// 2) string to sign
	scope := strings.Join([]string{dateStamp, region, service, sigv4Request}, "/")
	stringToSign := strings.Join([]string{
		sigv4Algorithm,
		amzDate,
		scope,
		hexSHA256([]byte(canonicalReq)),
	}, "\n")

	// 3) signing key + signature
	signingKey := deriveSigningKey(creds.SecretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// 4) Authorization 头
	auth := sigv4Algorithm +
		" Credential=" + creds.AccessKeyID + "/" + scope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + signature
	req.Header.Set("Authorization", auth)
}

// canonicalHeaders 返回 canonical headers 串与 signed headers 串。
// 只签 host、x-amz-* 头(x-amz-date / x-amz-security-token 等),按名字排序、值折叠空白。
func canonicalHeaders(req *http.Request) (canonical, signed string) {
	type kv struct{ name, value string }
	var hs []kv

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	hs = append(hs, kv{"host", host})

	for name, vals := range req.Header {
		ln := strings.ToLower(name)
		if strings.HasPrefix(ln, "x-amz-") {
			hs = append(hs, kv{ln, strings.Join(vals, ",")})
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].name < hs[j].name })

	var cb, sb strings.Builder
	for i, h := range hs {
		cb.WriteString(h.name)
		cb.WriteByte(':')
		cb.WriteString(trimAll(h.value))
		cb.WriteByte('\n')
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(h.name)
	}
	return cb.String(), sb.String()
}

// canonicalURI 对已转义的 path 做 SigV4 规范化。Bedrock 的 model id 含 ':' 等保留字符,
// Go 的 URL.EscapedPath() 已把它们编码(如 %3A);SigV4 要求 canonical URI 与实际请求 path 一致,
// 故这里原样使用 EscapedPath 的结果(空 path 归一为 "/")。
func canonicalURI(escapedPath string) string {
	if escapedPath == "" {
		return "/"
	}
	return escapedPath
}

// canonicalQuery 返回按 key 排序的 canonical query 串(Bedrock invoke 无 query 时为空串)。
func canonicalQuery(req *http.Request) string {
	q := req.URL.Query()
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := q[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// deriveSigningKey 逐级 HMAC 派生签名密钥:kDate→kRegion→kService→kSigning。
func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte(sigv4Request))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// trimAll 折叠 header value 里的连续空白为单个空格并去首尾空白(SigV4 要求)。
func trimAll(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// uriEncode 按 AWS SigV4 规则百分号编码:不编码 A-Za-z0-9-._~,其余编码为大写 %XX;
// encodeSlash=false 时不编码 '/'(用于 path 段,虽然本实现的 path 走 EscapedPath)。
func uriEncode(s string, encodeSlash bool) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0x0f])
		}
	}
	return b.String()
}
