package botguard

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Cipher 加密签名载荷。魔改算法实现此接口后用 WithCipher 注册为当前算法，
// 轮换期的旧算法用 WithAcceptCiphers 继续接受。
//
// 线上格式为 ID + "." + base64url(Seal 的输出)。同一 ID 的浏览器实现
// （BotGuard.registerCipher 的 seal）必须与 Open 逐字节兼容。
// 令牌密封始终用服务端 AES-GCM，不经过此接口。
type Cipher interface {
	// ID 是签名前缀，1~16 位字母或数字。
	ID() string
	// Seal 加密明文载荷。aad 是业务摘要，实现必须把它纳入完整性校验。
	Seal(key, plaintext, aad []byte) ([]byte, error)
	// Open 解密。密钥不对、摘要不符或格式非法都返回错误。
	Open(key, sealed, aad []byte) ([]byte, error)
}

// AESGCM 是默认算法，ID 为 "v1"：AES-256-GCM，输出为 12 字节随机 IV 接密文与 tag。
var AESGCM Cipher = aesGCM{}

type aesGCM struct{}

func (aesGCM) ID() string { return "v1" }

func (aesGCM) Seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, gcmNonceSize, gcmNonceSize+len(plaintext)+gcmTagSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	return aead.Seal(iv, iv, plaintext, aad), nil
}

func (aesGCM) Open(key, sealed, aad []byte) ([]byte, error) {
	if len(sealed) < gcmNonceSize+gcmTagSize {
		return nil, errors.New("botguard: ciphertext too short")
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, sealed[:gcmNonceSize], sealed[gcmNonceSize:], aad)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

const (
	gcmNonceSize = 12
	gcmTagSize   = 16
)

func validCipherID(id string) bool {
	if len(id) < 1 || len(id) > 16 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

func addCipher(m map[string]Cipher, c Cipher) error {
	if c == nil {
		return errors.New("botguard: nil cipher")
	}
	id := c.ID()
	if !validCipherID(id) {
		return fmt.Errorf("botguard: invalid cipher id %q", id)
	}
	if _, ok := m[id]; ok {
		return fmt.Errorf("botguard: duplicate cipher id %q", id)
	}
	m[id] = c
	return nil
}

func collectCiphers(o *options) (Cipher, map[string]Cipher, error) {
	active := o.cipher
	if active == nil {
		active = AESGCM
	}
	all := make(map[string]Cipher, 1+len(o.accept))
	if err := addCipher(all, active); err != nil {
		return nil, nil, err
	}
	for _, c := range o.accept {
		if err := addCipher(all, c); err != nil {
			return nil, nil, err
		}
	}
	return active, all, nil
}

func sealPayload(c Cipher, key []byte, p Payload, digest []byte) (string, error) {
	plain, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sealed, err := c.Seal(key, plain, digest)
	if err != nil {
		return "", err
	}
	return c.ID() + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func decodeSign(raw string) (string, []byte, error) {
	id, enc, ok := strings.Cut(raw, ".")
	if !ok || !validCipherID(id) || enc == "" || len(enc) > 8192 {
		return "", nil, errors.New("botguard: malformed signature")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(sealed) == 0 {
		return "", nil, errors.New("botguard: malformed signature")
	}
	return id, sealed, nil
}
