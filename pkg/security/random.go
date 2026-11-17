package security

import (
	"crypto/rand"
	"math/big"
	mrand "math/rand/v2"
)

// DefaultAlphabet 是 RandomString / PseudorandomString 使用的字符集(大小写字母 + 数字)。
const DefaultAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString 用 crypto/rand 生成长度为 n 的随机串(字符取自 DefaultAlphabet)。
// 可用于令牌、验证码、盐等安全场景。n <= 0 返回空串。
func RandomString(n int) string {
	return RandomStringWithAlphabet(n, DefaultAlphabet)
}

// RandomStringWithAlphabet 用 crypto/rand 从 alphabet 中均匀选取 n 个字符(按 rune 计)。
// alphabet 为空时 panic(属于调用方编程错误)。
func RandomStringWithAlphabet(n int, alphabet string) string {
	runes := alphabetRunes(alphabet)
	if n <= 0 {
		return ""
	}
	max := big.NewInt(int64(len(runes)))
	out := make([]rune, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand 自 Go 1.24 起不会返回错误,这里只为兜底。
			panic("security: crypto/rand failed: " + err.Error())
		}
		out[i] = runes[idx.Int64()]
	}
	return string(out)
}

// PseudorandomString 用 math/rand/v2 生成长度为 n 的随机串,速度快但**不可用于安全场景**。
func PseudorandomString(n int) string {
	return PseudorandomStringWithAlphabet(n, DefaultAlphabet)
}

// PseudorandomStringWithAlphabet 是 PseudorandomString 的自定义字符集版本。
// alphabet 为空时 panic。
func PseudorandomStringWithAlphabet(n int, alphabet string) string {
	runes := alphabetRunes(alphabet)
	if n <= 0 {
		return ""
	}
	out := make([]rune, n)
	for i := range out {
		out[i] = runes[mrand.IntN(len(runes))]
	}
	return string(out)
}

func alphabetRunes(alphabet string) []rune {
	runes := []rune(alphabet)
	if len(runes) == 0 {
		panic("security: empty alphabet")
	}
	return runes
}
