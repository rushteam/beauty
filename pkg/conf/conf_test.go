package conf_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/conf"
)

// --- file loader ---

func TestNew_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: beauty\nport: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loader, err := conf.New(path) // no scheme → file
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var cfg struct {
		Name string
		Port int
	}
	if err := loader.Unmarshal(&cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Name != "beauty" || cfg.Port != 8080 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestNew_UnknownScheme(t *testing.T) {
	_, err := conf.New("unknownscheme://host/key")
	if err == nil {
		t.Fatal("expected error for unknown scheme")
	}
}

func TestRegisterFactory_Duplicate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	stub := func(_ *url.URL) (conf.ConfigCenter, error) { return nil, nil }
	conf.RegisterFactory("dupscheme", stub)
	conf.RegisterFactory("dupscheme", stub) // must panic
}

// --- in-memory ConfigCenter stub ---

type memCC struct {
	val string
	ch  chan string
}

func newMemCC(initial string) *memCC {
	return &memCC{val: initial, ch: make(chan string, 4)}
}

func (m *memCC) Get(_ context.Context, _ string) (string, error) {
	return m.val, nil
}

func (m *memCC) Watch(_ context.Context, _ string, onChange func(string, string)) (context.CancelFunc, error) {
	go func() {
		for v := range m.ch {
			m.val = v
			onChange("key", v)
		}
	}()
	return func() { close(m.ch) }, nil
}

func (m *memCC) push(val string) { m.ch <- val }

func TestRemoteLoader_HotReload(t *testing.T) {
	cc := newMemCC("port: 9000\n")
	conf.RegisterFactory("memtest", func(_ *url.URL) (conf.ConfigCenter, error) {
		return cc, nil
	})

	loader, err := conf.New("memtest://host/config.yaml")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var cfg struct{ Port int }
	if err := loader.Unmarshal(&cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Port != 9000 {
		t.Fatalf("want 9000, got %d", cfg.Port)
	}

	// 热加载
	changed := make(chan struct{}, 1)
	loader.Watch(context.Background(), func() { changed <- struct{}{} })
	cc.push("port: 9999\n")

	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("hot reload callback not triggered")
	}

	var cfg2 struct{ Port int }
	if err := loader.Unmarshal(&cfg2); err != nil {
		t.Fatalf("Unmarshal after reload: %v", err)
	}
	if cfg2.Port != 9999 {
		t.Fatalf("want 9999 after reload, got %d", cfg2.Port)
	}
}

// 推送一份非法 YAML 时，loader 必须保留上一份可用配置（last-good），
// 且不触发变更回调，避免坏配置覆盖好配置导致后续 Unmarshal 全部失败。
func TestRemoteLoader_InvalidUpdateKeepsLastGood(t *testing.T) {
	cc := newMemCC("port: 9000\n")
	conf.RegisterFactory("memtestbad", func(_ *url.URL) (conf.ConfigCenter, error) {
		return cc, nil
	})

	loader, err := conf.New("memtestbad://host/config.yaml")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	changed := make(chan struct{}, 1)
	loader.Watch(context.Background(), func() { changed <- struct{}{} })

	// 非法 YAML（未闭合的流式序列），validate 应拒绝
	cc.push("port: [unclosed\n")

	select {
	case <-changed:
		t.Fatal("invalid config must not trigger change callback")
	case <-time.After(300 * time.Millisecond):
		// 预期：回调未触发
	}

	var cfg struct{ Port int }
	if err := loader.Unmarshal(&cfg); err != nil {
		t.Fatalf("Unmarshal should still work with last-good: %v", err)
	}
	if cfg.Port != 9000 {
		t.Fatalf("want last-good 9000 after invalid push, got %d", cfg.Port)
	}
}
