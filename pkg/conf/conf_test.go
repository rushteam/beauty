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
