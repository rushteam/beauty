package botguard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"time"
)

var (
	tokenAAD        = []byte("botguard/token/v1")
	errTokenInvalid = errors.New("botguard: invalid token")
)

type tokenClaims struct {
	ID          string `json:"id"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
	IPNet       string `json:"ip,omitempty"`
	Fingerprint string `json:"fp,omitempty"`
}

// Grant 是下发给客户端的令牌与会话密钥。
type Grant struct {
	Token     string
	Key       []byte // 会话密钥（AES-256），客户端用它加密签名载荷
	ExpiresAt time.Time
}

// IssueToken 为当前请求签发令牌，绑定客户端 IP 段与设备指纹。
// 浏览器走 TokenHandler；原生 App 等可在自己的接口里直接调用。
func (g *Guard) IssueToken(r *http.Request, fingerprint string) (Grant, error) {
	now := time.Now()
	c := tokenClaims{
		ID:          rand.Text(),
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(g.o.tokenTTL).Unix(),
		IPNet:       ipPrefix(g.o.clientIP(r)),
		Fingerprint: fingerprint,
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return Grant{}, err
	}
	ks := g.keys[0]
	return Grant{
		Token:     base64.RawURLEncoding.EncodeToString(ks.token.Seal(nil, nil, raw, tokenAAD)),
		Key:       sessionKey(ks.sess, c.ID),
		ExpiresAt: time.Unix(c.ExpiresAt, 0),
	}, nil
}

func (g *Guard) openToken(tok string) (*tokenClaims, *keyset, error) {
	if len(tok) > 1024 {
		return nil, nil, errTokenInvalid
	}
	sealed, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, nil, errTokenInvalid
	}
	for i := range g.keys {
		raw, err := g.keys[i].token.Open(nil, nil, sealed, tokenAAD)
		if err != nil {
			continue
		}
		var c tokenClaims
		if json.Unmarshal(raw, &c) != nil || c.ID == "" {
			return nil, nil, errTokenInvalid
		}
		return &c, &g.keys[i], nil
	}
	return nil, nil, errTokenInvalid
}

// TokenHandler 令牌下发接口（POST，body: {"fp":"<指纹哈希>"}）。
// 响应 {"token","key","exp","now"}，now 为服务端毫秒时间，SDK 用它校准时钟。
// 该接口本身应叠加 IP 级限流（pkg/middleware/ratelimit），防止批量刷令牌。
func (g *Guard) TokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed"})
			return
		}
		var req struct {
			Fingerprint string `json:"fp"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || len(req.Fingerprint) > 128 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		grant, err := g.IssueToken(r, req.Fingerprint)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"token": grant.Token,
			"key":   base64.RawURLEncoding.EncodeToString(grant.Key),
			"exp":   grant.ExpiresAt.Unix(),
			"now":   time.Now().UnixMilli(),
		})
	})
}

func sessionKey(root []byte, tokenID string) []byte {
	mac := hmac.New(sha256.New, root)
	mac.Write([]byte(tokenID))
	return mac.Sum(nil)
}

// ipPrefix 把 IP 归一到网段（IPv4 /24、IPv6 /48），容忍同一网络内的地址漂移。
func ipPrefix(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	p, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}
