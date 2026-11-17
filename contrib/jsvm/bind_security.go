package jsvm

import (
	"time"

	"github.com/dop251/goja"

	"github.com/rushteam/beauty/pkg/security"
)

func (inst *instance) bindSecurity() error {
	rt := inst.rt
	alphabet := func(fn string, a string) {
		if a == "" {
			panic(rt.NewTypeError(fn + ": alphabet must not be empty"))
		}
	}
	obj := rt.NewObject()
	if err := setAll(obj, map[string]any{
		"randomString": security.RandomString,
		"randomStringWithAlphabet": func(n int, a string) string {
			alphabet("randomStringWithAlphabet", a)
			return security.RandomStringWithAlphabet(n, a)
		},
		"pseudorandomString": security.PseudorandomString,
		"pseudorandomStringWithAlphabet": func(n int, a string) string {
			alphabet("pseudorandomStringWithAlphabet", a)
			return security.PseudorandomStringWithAlphabet(n, a)
		},
		"md5":    security.MD5,
		"sha256": security.SHA256,
		"sha512": security.SHA512,
		"hs256":  security.HS256,
		"hs512":  security.HS512,
		"equal":  security.Equal,
		"encrypt": func(data goja.Value, key string) (string, error) {
			b, ok := inst.toBytes(data)
			if !ok {
				panic(rt.NewTypeError("$security.encrypt: data must be a string or binary"))
			}
			return security.Encrypt(b, []byte(key))
		},
		"decrypt": func(cipherText, key string) (string, error) {
			b, err := security.Decrypt(cipherText, []byte(key))
			return string(b), err
		},
		"hashPassword":  security.HashPassword,
		"checkPassword": security.CheckPassword,
		// createJWT(payload, secret, durationSeconds):durationSeconds <= 0 表示不设 exp。
		"createJWT": func(payload map[string]any, secret string, durationSec float64) (string, error) {
			return security.CreateJWT(payload, []byte(secret), time.Duration(durationSec*float64(time.Second)))
		},
		"parseJWT": func(token, secret string) (map[string]any, error) {
			return security.ParseJWT(token, []byte(secret))
		},
		"parseUnverifiedJWT": security.ParseUnverifiedJWT,
	}); err != nil {
		return err
	}
	return rt.Set("$security", obj)
}
