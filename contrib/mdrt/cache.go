package mdrt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// cacheEntry 是一条已经验收过的函数体。
type cacheEntry struct {
	Body  string    `json:"body"`
	Model string    `json:"model"`
	Usage llm.Usage `json:"usage"`
	Time  time.Time `json:"time"`
}

type diskCache struct {
	dir string
}

func (c diskCache) path(key string) string {
	return filepath.Join(c.dir, key+".json")
}

func (c diskCache) Get(key string) (cacheEntry, bool) {
	if c.dir == "" || key == "" {
		return cacheEntry{}, false
	}
	raw, err := os.ReadFile(c.path(key))
	if err != nil {
		return cacheEntry{}, false
	}
	var ent cacheEntry
	if err := json.Unmarshal(raw, &ent); err != nil || strings.TrimSpace(ent.Body) == "" {
		return cacheEntry{}, false
	}
	return ent, true
}

func (c diskCache) Put(key string, ent cacheEntry) error {
	if c.dir == "" || key == "" {
		return nil
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(ent, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(c.path(key), raw)
}

// CacheKey 由方言、prelude、提示词、模型、单元种类、行为文本和入参决定。例子不参与。
func CacheKey(model string, unit Unit) string {
	if unit.Behavior == nil {
		return ""
	}
	raw, _ := json.Marshal(canonInputs(unit.Inputs))
	payload := strings.Join([]string{
		Dialect,
		PreludeVersion,
		PromptVersion,
		model,
		unit.Kind,
		normalizeText(unit.Behavior.Body),
		string(raw),
	}, "\n")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r\n", "\n")), " ")
}

type canonField struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	In       string   `json:"in"`
	Optional bool     `json:"optional"`
	GT       *float64 `json:"gt,omitempty"`
	GTE      *float64 `json:"gte,omitempty"`
	LT       *float64 `json:"lt,omitempty"`
	LTE      *float64 `json:"lte,omitempty"`
	MinLen   *int     `json:"minLen,omitempty"`
	MaxLen   *int     `json:"maxLen,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
}

func canonInputs(fields []Field) []canonField {
	out := make([]canonField, 0, len(fields))
	for _, f := range fields {
		out = append(out, canonField{
			Name: f.Name, Type: f.Type, In: f.In, Optional: f.Optional,
			GT: f.GT, GTE: f.GTE, LT: f.LT, LTE: f.LTE,
			MinLen: f.MinLen, MaxLen: f.MaxLen, Enum: f.Enum, Pattern: f.Pattern,
		})
	}
	return out
}

func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
