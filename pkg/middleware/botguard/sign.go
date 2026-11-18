package botguard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// CanonicalDigest 计算请求业务摘要：
//
//	SHA256(METHOD "\n" path "\n" sortedQuery "\n" hex(SHA256(body)))
//
// path 为解码后的路径；sortedQuery 为每个 k=v 按 encodeURIComponent 规则转义后
// 按字典序排序、以 & 连接。浏览器 SDK 使用完全相同的规则。
func CanonicalDigest(method, path string, query url.Values, body []byte) []byte {
	pairs := make([]string, 0, len(query))
	for k, vs := range query {
		for _, v := range vs {
			pairs = append(pairs, uriComponent(k)+"="+uriComponent(v))
		}
	}
	sort.Strings(pairs)
	bh := sha256.Sum256(body)

	h := sha256.New()
	h.Write([]byte(strings.ToUpper(method)))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write([]byte(strings.Join(pairs, "&")))
	h.Write([]byte{'\n'})
	h.Write([]byte(hex.EncodeToString(bh[:])))
	return h.Sum(nil)
}

// uriComponent 与 JS encodeURIComponent 一致：仅 A-Z a-z 0-9 - _ . ! ~ * ' ( ) 不转义。
func uriComponent(s string) string {
	const hexUpper = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&15])
		}
	}
	return b.String()
}

// SealPayload 用默认算法 AESGCM 加密载荷，返回签名 header 值（"v1." + base64url）。
// 自定义算法用 (*Guard).SealPayload。
func SealPayload(key []byte, p Payload, digest []byte) (string, error) {
	return sealPayload(AESGCM, key, p, digest)
}

// SignRequest 用默认算法 AESGCM 和默认 header 名给请求加签，并在读取 body 后复原。
// p.Nonce 为空时自动生成；p.TS 需由调用方设置。
// 自定义算法或自定义 header 名用 (*Guard).SignRequest。
func SignRequest(r *http.Request, g Grant, p Payload) error {
	return signRequest(r, AESGCM, "X-Guard-Token", "X-Guard-Sign", g, p)
}

// SealPayload 用当前算法加密载荷。
func (g *Guard) SealPayload(key []byte, p Payload, digest []byte) (string, error) {
	return sealPayload(g.cipher, key, p, digest)
}

// SignRequest 用当前算法和已配置的 header 名给请求加签。
func (g *Guard) SignRequest(r *http.Request, grant Grant, p Payload) error {
	return signRequest(r, g.cipher, g.o.tokenHeader, g.o.signHeader, grant, p)
}

func signRequest(r *http.Request, c Cipher, tokenHeader, signHeader string, grant Grant, p Payload) error {
	body, fail := readBody(r, 1<<30)
	if fail != "" {
		return errors.New("botguard: " + fail)
	}
	if p.Nonce == "" {
		p.Nonce = rand.Text()
	}
	sig, err := sealPayload(c, grant.Key, p, CanonicalDigest(r.Method, r.URL.Path, r.URL.Query(), body))
	if err != nil {
		return err
	}
	r.Header.Set(tokenHeader, grant.Token)
	r.Header.Set(signHeader, sig)
	return nil
}
