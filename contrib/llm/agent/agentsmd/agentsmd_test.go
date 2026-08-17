package agentsmd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/agentsmd"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollect_RootToCwdOrder(t *testing.T) {
	root := t.TempDir()
	// 模拟仓库: root/.git + AGENTS.md, sub/AGENTS.md, sub/pkg/(无)
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main")
	write(t, filepath.Join(root, "AGENTS.md"), "ROOT rules")
	write(t, filepath.Join(root, "sub", "AGENTS.md"), "SUB rules")
	cwd := filepath.Join(root, "sub", "pkg")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	p := agentsmd.New(cwd)
	parts, err := p.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %v, want 2", parts)
	}
	if parts[0] != "ROOT rules" || parts[1] != "SUB rules" {
		t.Fatalf("order = %v, want root→cwd", parts)
	}
}

func TestCollect_StopAtGit(t *testing.T) {
	outer := t.TempDir()
	write(t, filepath.Join(outer, "AGENTS.md"), "OUTER should not appear")
	repo := filepath.Join(outer, "repo")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	write(t, filepath.Join(repo, "AGENTS.md"), "REPO")
	cwd := filepath.Join(repo, "pkg")
	_ = os.MkdirAll(cwd, 0o755)

	p := agentsmd.New(cwd)
	parts, err := p.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0] != "REPO" {
		t.Fatalf("parts = %v, want only REPO (stop at .git)", parts)
	}
}

func TestCollect_ExplicitRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "AGENTS.md"), "A")
	mid := filepath.Join(root, "mid")
	write(t, filepath.Join(mid, "AGENTS.md"), "B")
	cwd := filepath.Join(mid, "leaf")
	_ = os.MkdirAll(cwd, 0o755)
	write(t, filepath.Join(cwd, "AGENTS.md"), "C")

	stop := false
	p := &agentsmd.Provider{Dir: cwd, Root: mid, StopAtGit: &stop}
	parts, err := p.Collect()
	if err != nil {
		t.Fatal(err)
	}
	// Root=mid → mid + leaf, 不含 outer root
	if len(parts) != 2 || parts[0] != "B" || parts[1] != "C" {
		t.Fatalf("parts = %v, want [B C]", parts)
	}
}

func TestInvoking_AppendsSystem(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	write(t, filepath.Join(root, "AGENTS.md"), "use gofmt")
	cwd := filepath.Join(root, "pkg")
	_ = os.MkdirAll(cwd, 0o755)

	p := agentsmd.New(cwd)
	req := &llm.Request{System: "base"}
	msgs, tools, err := p.Invoking(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 || len(tools) != 0 {
		t.Fatalf("unexpected msgs/tools")
	}
	if !strings.Contains(req.System, "base") || !strings.Contains(req.System, "use gofmt") {
		t.Fatalf("system = %q", req.System)
	}
	if !strings.Contains(req.System, "由通用到具体") {
		t.Fatalf("missing header: %q", req.System)
	}
}

func TestInvoking_NoFile(t *testing.T) {
	dir := t.TempDir()
	p := agentsmd.New(dir)
	req := &llm.Request{System: "only"}
	_, _, err := p.Invoking(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if req.System != "only" {
		t.Fatalf("system mutated: %q", req.System)
	}
}
