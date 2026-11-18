package botguard

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// Challenge 是无感工作量证明挑战：客户端需找到 counter，
// 使 SHA256(challenge + ":" + counter) 的前导零比特数 >= Bits，
// 然后在 PoW header 中携带 "challenge:counter" 重发请求。
type Challenge struct {
	Type      string `json:"type"`
	Challenge string `json:"challenge"`
	Bits      int    `json:"bits"`
	ExpiresAt int64  `json:"exp"`
}

// NewChallenge 为令牌签发一个无状态 PoW 挑战（HMAC 签名，绑定令牌与过期时间）。
// 自定义 ChallengeHandler 时也可调用它。
func (g *Guard) NewChallenge(tokenID string) Challenge {
	exp := time.Now().Add(g.o.powTTL).Unix()
	body := tokenID + "|" + strconv.FormatInt(exp, 10) + "|" + strconv.Itoa(g.o.powBits) + "|" + rand.Text()[:8]
	enc := base64.RawURLEncoding.EncodeToString([]byte(body))
	return Challenge{
		Type:      "pow",
		Challenge: enc + "." + powMAC(g.keys[0].pow, enc),
		Bits:      g.o.powBits,
		ExpiresAt: exp,
	}
}

func (g *Guard) verifyPoW(ctx context.Context, header, tokenID string, now time.Time) bool {
	i := strings.LastIndexByte(header, ':')
	if i < 0 || len(header)-i-1 > 20 {
		return false
	}
	ch, counter := header[:i], header[i+1:]
	if _, err := strconv.ParseUint(counter, 10, 64); err != nil {
		return false
	}
	enc, mac, ok := strings.Cut(ch, ".")
	if !ok {
		return false
	}
	valid := false
	for _, ks := range g.keys {
		if hmac.Equal([]byte(mac), []byte(powMAC(ks.pow, enc))) {
			valid = true
			break
		}
	}
	if !valid {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || parts[0] != tokenID {
		return false
	}
	exp, err1 := strconv.ParseInt(parts[1], 10, 64)
	need, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || now.Unix() > exp {
		return false
	}
	if leadingZeroBits(sha256.Sum256([]byte(header))) < need {
		return false
	}
	// 每个挑战只能兑换一次
	fresh, err := g.store.SetNX(ctx, "bg:p:"+mac, []byte{1}, time.Unix(exp, 0).Sub(now)+time.Second)
	return err == nil && fresh
}

// SolveChallenge 求解挑战，返回可直接放入 PoW header 的值。供非浏览器客户端与测试使用。
func SolveChallenge(c Challenge) string {
	prefix := c.Challenge + ":"
	for n := uint64(0); ; n++ {
		h := prefix + strconv.FormatUint(n, 10)
		if leadingZeroBits(sha256.Sum256([]byte(h))) >= c.Bits {
			return h
		}
	}
}

func powMAC(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

func leadingZeroBits(sum [32]byte) int {
	n := 0
	for _, b := range sum {
		if b != 0 {
			return n + bits.LeadingZeros8(b)
		}
		n += 8
	}
	return n
}
