// Package security 收拢业务里最常用、又最容易写错的安全原语:随机串、摘要/HMAC、
// 常量时间比较、AES-GCM 对称加密、bcrypt 密码哈希、通用 JWT。
//
// 设计要点:
//   - 随机串默认走 crypto/rand(令牌、验证码、盐都能用);Pseudorandom* 走 math/rand/v2,
//     只用于非安全场景(文件名、展示用 id 等),换取速度;
//   - Encrypt/Decrypt 用 AES-GCM + 随机 nonce,输出 base64(nonce||密文||tag),带完整性校验;
//   - 密码哈希用 bcrypt,注意 bcrypt 只取前 72 字节,超长密码会返回错误而不是静默截断;
//   - JWT 只签/验 HMAC(HS256/HS512),解析时锁定算法,杜绝 alg 混淆(none / RS→HS)。
//
// 与 pkg/api/token 的分工:token 管"会话令牌全生命周期"(双 token、续签、黑名单);
// 本包的 CreateJWT/ParseJWT 是不带策略的通用签发/校验(邮件链接、一次性票据等)。
//
// 所有函数无状态、并发安全。
package security

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"hash"
)

// MD5 返回 s 的 MD5 十六进制摘要。MD5 已不抗碰撞,只用于校验和/缓存键,勿用于安全签名。
func MD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SHA256 返回 s 的 SHA-256 十六进制摘要。
func SHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SHA512 返回 s 的 SHA-512 十六进制摘要。
func SHA512(s string) string {
	sum := sha512.Sum512([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HS256 返回以 secret 为密钥的 HMAC-SHA256 十六进制签名。
func HS256(text, secret string) string {
	return hmacHex(sha256.New, text, secret)
}

// HS512 返回以 secret 为密钥的 HMAC-SHA512 十六进制签名。
func HS512(text, secret string) string {
	return hmacHex(sha512.New, text, secret)
}

func hmacHex(h func() hash.Hash, text, secret string) string {
	m := hmac.New(h, []byte(secret))
	m.Write([]byte(text))
	return hex.EncodeToString(m.Sum(nil))
}

// Equal 以常量时间比较两个字符串,用于比对签名、令牌等秘密值,防止计时侧信道。
// 长度不同时立即返回 false(只泄露长度)。
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
