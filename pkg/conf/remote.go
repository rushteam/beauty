package conf

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
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
	if err := r.validate(val); err != nil {
		return err
	}
	r.mu.Lock()
	r.current = val
	r.mu.Unlock()
	return nil
}

// validate 用配置格式试解析一遍，确保内容可用。
func (r *remoteLoader) validate(raw string) error {
	v := viper.New()
	v.SetConfigType(r.format)
	if err := v.ReadConfig(bytes.NewBufferString(raw)); err != nil {
		return fmt.Errorf("conf: invalid %s content for key %q: %w", r.format, r.key, err)
	}
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
		// 先校验再提交：坏内容（如配置中心被误推一份非法 YAML）不应覆盖
		// 当前可用配置，否则后续 Unmarshal 全部失败且无法回滚到 last-good。
		if err := r.validate(value); err != nil {
			slog.Warn("conf: ignored invalid remote config update, keeping last-good",
				"key", r.key, "err", err)
			return
		}
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
