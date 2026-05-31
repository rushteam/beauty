package conf

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

// remoteLoader 使用 ConfigCenter 拉取配置，并通过 Watch 实现热加载。
// Unmarshal 每次都从最新内容重新解析，保证热加载后的调用拿到新值。
type remoteLoader struct {
	cc      ConfigCenter
	key     string
	format  string // yaml / json / toml …

	mu      sync.RWMutex
	current string // 最新原始内容
}

func newRemoteLoader(cc ConfigCenter, key, format string) *remoteLoader {
	return &remoteLoader{cc: cc, key: key, format: format}
}

func (r *remoteLoader) load(ctx context.Context) error {
	val, err := r.cc.Get(ctx, r.key)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.current = val
	r.mu.Unlock()
	return nil
}

func (r *remoteLoader) Unmarshal(dst any) error {
	r.mu.RLock()
	raw := r.current
	r.mu.RUnlock()

	v := viper.New()
	v.SetConfigType(r.format)
	if err := v.ReadConfig(bytes.NewBufferString(raw)); err != nil {
		return fmt.Errorf("conf: unmarshal: %w", err)
	}
	return v.Unmarshal(dst)
}

func (r *remoteLoader) Watch(ctx context.Context, fn func()) {
	_, _ = r.cc.Watch(ctx, r.key, func(_, value string) {
		r.mu.Lock()
		r.current = value
		r.mu.Unlock()
		fn()
	})
}

// inferFormat 从 key 的扩展名推断配置格式，兜底 yaml。
func inferFormat(key string) string {
	ext := strings.TrimPrefix(filepath.Ext(key), ".")
	switch ext {
	case "yaml", "yml", "json", "toml", "ini", "properties":
		return ext
	}
	return "yaml"
}
