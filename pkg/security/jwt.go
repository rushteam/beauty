package security

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// CreateJWT 用 HS256 签发 JWT。ttl > 0 时写入 exp(当前时间 + ttl),并总是写入 iat;
// claims 里已有的 exp/iat 会被覆盖。secret 为空返回错误。
func CreateJWT(claims map[string]any, secret []byte, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("security: empty JWT secret")
	}
	mc := make(jwt.MapClaims, len(claims)+2)
	for k, v := range claims {
		mc[k] = v
	}
	now := time.Now()
	mc["iat"] = now.Unix()
	if ttl > 0 {
		mc["exp"] = now.Add(ttl).Unix()
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, mc).SignedString(secret)
}

// ParseJWT 校验签名(仅接受 HS256/HS512)与 exp/nbf/iat 后返回 claims。
func ParseJWT(token string, secret []byte) (map[string]any, error) {
	if len(secret) == 0 {
		return nil, errors.New("security: empty JWT secret")
	}
	mc := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, mc, func(*jwt.Token) (any, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256", "HS512"}))
	if err != nil {
		return nil, err
	}
	return mc, nil
}

// ParseUnverifiedJWT 只解码不验签,用于在验签前读取 kid/iss 等路由信息。
// **返回的 claims 不可信**,不得用于鉴权决策。
func ParseUnverifiedJWT(token string) (map[string]any, error) {
	mc := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, mc); err != nil {
		return nil, err
	}
	return mc, nil
}
