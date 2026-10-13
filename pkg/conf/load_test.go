package conf_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rushteam/beauty/pkg/conf"
)

func TestFileFromEnv(t *testing.T) {
	t.Run("env set", func(t *testing.T) {
		t.Setenv("CONFIG_FILE", "/etc/app/config.yaml")
		if got := conf.FileFromEnv("fallback.yaml"); got != "/etc/app/config.yaml" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("env not set", func(t *testing.T) {
		if got := conf.FileFromEnv("fallback.yaml"); got != "fallback.yaml" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("env empty string", func(t *testing.T) {
		t.Setenv("CONFIG_FILE", "")
		if got := conf.FileFromEnv("fallback.yaml"); got != "fallback.yaml" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLoad_EmptyPath(t *testing.T) {
	var cfg struct{ Name string }
	if err := conf.Load("", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "" {
		t.Fatalf("expected zero value, got %q", cfg.Name)
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	var cfg struct{ Name string }
	if err := conf.Load("/nonexistent/path/config.yaml", &cfg); err != nil {
		t.Fatalf("expected nil for missing file, got %v", err)
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, []byte("name: beauty\nport: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		Name string
		Port int
	}
	if err := conf.Load(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "beauty" || cfg.Port != 8080 {
		t.Fatalf("unexpected: %+v", cfg)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg struct{ Name string }
	if err := conf.Load(path, &cfg); err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

func TestLoad_StrictFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	// typo_field 在结构体中不存在
	if err := os.WriteFile(path, []byte("name: beauty\ntypo_field: oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	type cfg struct{ Name string }

	// 默认（非严格）：多余字段被忽略
	var c1 cfg
	if err := conf.Load(path, &c1); err != nil {
		t.Fatalf("non-strict should pass: %v", err)
	}
	if c1.Name != "beauty" {
		t.Fatalf("Name=%q", c1.Name)
	}

	// 严格：多余字段报错
	var c2 cfg
	if err := conf.Load(path, &c2, conf.WithStrictFields()); err == nil {
		t.Fatal("strict should fail on unknown field")
	}
}

func TestLoad_WithSecrets(t *testing.T) {
	t.Setenv("BEAUTY_LOAD_TOKEN", "s3cret")
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	content := "token: \"${env:BEAUTY_LOAD_TOKEN}\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg struct{ Token string }
	if err := conf.Load(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "s3cret" {
		t.Fatalf("Token=%q, want s3cret", cfg.Token)
	}
}
