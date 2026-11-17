package security

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrDecrypt 表示密文无法解密(格式错误、密钥不对或被篡改)。
var ErrDecrypt = errors.New("security: decrypt failed")

// Encrypt 用 AES-GCM(随机 nonce)加密 plaintext,返回 base64(nonce||密文||tag)。
// key 长度必须是 16 / 24 / 32 字节(对应 AES-128/192/256)。
// 每次调用 nonce 不同,同一明文多次加密结果也不同。
func Encrypt(plaintext, key []byte) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nil, nil, plaintext, nil)), nil
}

// Decrypt 解密 Encrypt 的输出。密钥不对、密文被篡改或格式非法都返回 ErrDecrypt。
func Decrypt(ciphertext string, key []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	plain, err := aead.Open(nil, nil, raw, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("security: invalid AES key (need 16/24/32 bytes): %w", err)
	}
	return cipher.NewGCMWithRandomNonce(block)
}
