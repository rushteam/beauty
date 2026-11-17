package mdrt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrozenSnapshot(t *testing.T) {
	dir := t.TempDir()
	if _, err := Build(t.Context(),
		WithSpecDir("example/specs"),
		WithStateDir(dir),
		WithLockFile(filepath.Join(dir, "lock.json")),
		WithJWTSecret("test-secret"),
	); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "gen")
	dst := filepath.Join("example", "frozen", "gen")
	if os.Getenv("MDRT_WRITE_FROZEN") == "1" {
		if err := os.RemoveAll(dst); err != nil {
			t.Fatal(err)
		}
		if err := copyDir(src, dst); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		got, err := os.ReadFile(filepath.Join(dst, e.Name()))
		if err != nil {
			t.Fatalf("缺少冻结产物 %s,先 MDRT_WRITE_FROZEN=1 go test -run TestFrozenSnapshot", e.Name())
		}
		want, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("冻结产物 %s 与当前下降结果不一致", e.Name())
		}
	}
}

func TestFrozenExampleOmitsLLM(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./example/frozen").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "contrib/llm") {
		t.Fatal("冻结示例不应链接 contrib/llm")
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}
