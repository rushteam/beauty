package agui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// generateID 生成带前缀的唯一 ID。
func generateID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}
