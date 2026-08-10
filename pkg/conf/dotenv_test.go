package conf_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rushteam/beauty/pkg/conf"
)

func writeDotEnv(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "BEAUTY_DOT_A=hello\nBEAUTY_DOT_B=world\n")

	if err := conf.LoadDotEnv(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BEAUTY_DOT_A"); got != "hello" {
		t.Fatalf("BEAUTY_DOT_A=%q", got)
	}
	if got := os.Getenv("BEAUTY_DOT_B"); got != "world" {
		t.Fatalf("BEAUTY_DOT_B=%q", got)
	}
}

func TestLoadDotEnv_NoOverwrite(t *testing.T) {
	t.Setenv("BEAUTY_DOT_EXIST", "original")
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "BEAUTY_DOT_EXIST=replaced\n")

	if err := conf.LoadDotEnv(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BEAUTY_DOT_EXIST"); got != "original" {
		t.Fatalf("expected original, got %q", got)
	}
}

func TestOverloadDotEnv(t *testing.T) {
	t.Setenv("BEAUTY_DOT_OVR", "old")
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "BEAUTY_DOT_OVR=new\n")

	if err := conf.OverloadDotEnv(filepath.Join(dir, ".env")); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BEAUTY_DOT_OVR"); got != "new" {
		t.Fatalf("expected new, got %q", got)
	}
}

func TestLoadDotEnv_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "BEAUTY_DOT_M=base\nBEAUTY_DOT_N=only-base\n")
	writeDotEnv(t, dir, ".env.local", "BEAUTY_DOT_M=local\n")

	if err := conf.OverloadDotEnv(
		filepath.Join(dir, ".env"),
		filepath.Join(dir, ".env.local"),
	); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BEAUTY_DOT_M"); got != "local" {
		t.Fatalf("BEAUTY_DOT_M=%q, want local", got)
	}
	if got := os.Getenv("BEAUTY_DOT_N"); got != "only-base" {
		t.Fatalf("BEAUTY_DOT_N=%q", got)
	}
}

func TestDotEnvProvider(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "DB_HOST=localhost\nDB_PORT=5432\n")

	provider := conf.DotEnvProvider(filepath.Join(dir, ".env"))
	if provider.Scheme() != "dotenv" {
		t.Fatalf("scheme=%q", provider.Scheme())
	}

	ctx := context.Background()
	got, err := conf.ExpandString(ctx, "host=${dotenv:DB_HOST}:${dotenv:DB_PORT}", provider)
	if err != nil {
		t.Fatal(err)
	}
	if got != "host=localhost:5432" {
		t.Fatalf("got %q", got)
	}
}

func TestDotEnvProvider_Missing(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "K=V\n")

	provider := conf.DotEnvProvider(filepath.Join(dir, ".env"))
	_, err := conf.ExpandString(context.Background(), "${dotenv:NOPE}", provider)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestDotEnvProvider_FileNotExist(t *testing.T) {
	provider := conf.DotEnvProvider("/nonexistent/.env")
	got, err := conf.ExpandString(context.Background(), "${dotenv:X:-fallback}", provider)
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestDotEnvProvider_WithSecrets(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "MY_SECRET=s3cret\n")

	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("token: \"${dotenv:MY_SECRET}\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	loader, err := conf.New(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	loader = conf.WithSecrets(loader, conf.DotEnvProvider(filepath.Join(dir, ".env")))

	var cfg struct {
		Token string
	}
	if err := loader.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "s3cret" {
		t.Fatalf("Token=%q", cfg.Token)
	}
}

func TestDotEnvProvider_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeDotEnv(t, dir, ".env", "A=1\nB=2\n")
	writeDotEnv(t, dir, ".env.local", "B=99\nC=3\n")

	provider := conf.DotEnvProvider(
		filepath.Join(dir, ".env"),
		filepath.Join(dir, ".env.local"),
	)

	ctx := context.Background()
	got, err := conf.ExpandString(ctx, "${dotenv:A}-${dotenv:B}-${dotenv:C}", provider)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1-99-3" {
		t.Fatalf("got %q", got)
	}
}
