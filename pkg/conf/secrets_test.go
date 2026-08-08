package conf_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rushteam/beauty/pkg/conf"
)

func TestExpandString_Env(t *testing.T) {
	t.Setenv("BEAUTY_CONF_SECRET", "s3cr3t")
	got, err := conf.ExpandString(context.Background(), "pw=${env:BEAUTY_CONF_SECRET}", conf.EnvProvider())
	if err != nil {
		t.Fatal(err)
	}
	if got != "pw=s3cr3t" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandString_EnvDefault(t *testing.T) {
	got, err := conf.ExpandString(context.Background(), "${env:MISSING_VAR_XYZ:-fallback}", conf.EnvProvider())
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandString_StrictMissing(t *testing.T) {
	_, err := conf.ExpandString(context.Background(), "${env:MISSING_VAR_XYZ}", conf.EnvProvider())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExpandString_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := conf.ExpandString(context.Background(), "${file:"+path+"}", conf.FileProvider())
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-file" {
		t.Fatalf("got %q", got)
	}
}

func TestExpand_Struct(t *testing.T) {
	t.Setenv("BEAUTY_DB_PASS", "p@ss")
	type cfg struct {
		DSN string
		Nested struct {
			Token string
		}
		Tags []string
	}
	c := &cfg{
		DSN: "user:${env:BEAUTY_DB_PASS}@tcp(localhost)/db",
		Nested: struct {
			Token string
		}{Token: "${secret:api}"},
		Tags: []string{"${env:BEAUTY_DB_PASS}", "plain"},
	}
	err := conf.Expand(context.Background(), c,
		conf.EnvProvider(),
		conf.MapProvider("secret", map[string]string{"api": "tok"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.DSN != "user:p@ss@tcp(localhost)/db" {
		t.Fatalf("DSN=%q", c.DSN)
	}
	if c.Nested.Token != "tok" {
		t.Fatalf("Token=%q", c.Nested.Token)
	}
	if c.Tags[0] != "p@ss" || c.Tags[1] != "plain" {
		t.Fatalf("Tags=%v", c.Tags)
	}
}

func TestWithSecrets_Loader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("BEAUTY_NOTIFY_SECRET", "notify-ok")
	content := "name: demo\nsecret: \"${env:BEAUTY_NOTIFY_SECRET}\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	loader, err := conf.New(path)
	if err != nil {
		t.Fatal(err)
	}
	loader = conf.WithSecrets(loader, conf.EnvProvider())

	var cfg struct {
		Name   string
		Secret string
	}
	if err := loader.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "demo" || cfg.Secret != "notify-ok" {
		t.Fatalf("unexpected: %+v", cfg)
	}
}
