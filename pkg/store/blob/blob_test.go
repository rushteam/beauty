package blob_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/store/blob"
)

func buckets(t *testing.T) map[string]blob.Bucket {
	t.Helper()
	local, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	return map[string]blob.Bucket{"local": local, "memory": blob.NewMemory()}
}

func listKeys(t *testing.T, b blob.Bucket, prefix string) []string {
	t.Helper()
	var keys []string
	for a, err := range b.List(context.Background(), prefix) {
		if err != nil {
			t.Fatalf("list %q: %v", prefix, err)
		}
		keys = append(keys, a.Key)
	}
	slices.Sort(keys)
	return keys
}

func TestBucket_Conformance(t *testing.T) {
	ctx := context.Background()
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := b.Stat(ctx, "missing.txt"); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("stat missing: %v", err)
			}
			if err := blob.PutBytes(ctx, b, "docs/a.txt", []byte("hello"), nil); err != nil {
				t.Fatal(err)
			}
			got, err := blob.GetBytes(ctx, b, "docs/a.txt")
			if err != nil || string(got) != "hello" {
				t.Fatalf("get = %q, %v", got, err)
			}
			a, err := b.Stat(ctx, "docs/a.txt")
			if err != nil || a.Size != 5 || !strings.HasPrefix(a.ContentType, "text/plain") {
				t.Fatalf("stat = %+v, %v", a, err)
			}

			// 无扩展名 → 嗅探;自定义 metadata 要能读回
			meta := map[string]string{"owner": "u1"}
			if err := blob.PutBytes(ctx, b, "docs/page", []byte("<html><body>x</body></html>"), &blob.PutOptions{Metadata: meta}); err != nil {
				t.Fatal(err)
			}
			a, _ = b.Stat(ctx, "docs/page")
			if !strings.HasPrefix(a.ContentType, "text/html") || a.Metadata["owner"] != "u1" {
				t.Fatalf("sniff/meta = %+v", a)
			}
			meta["owner"] = "mutated"
			if a, _ = b.Stat(ctx, "docs/page"); a.Metadata["owner"] != "u1" {
				t.Fatal("调用方修改 metadata 不应影响已存对象")
			}

			// 覆盖写
			if err := blob.PutBytes(ctx, b, "docs/a.txt", []byte("v2"), &blob.PutOptions{ContentType: "application/x-custom"}); err != nil {
				t.Fatal(err)
			}
			a, _ = b.Stat(ctx, "docs/a.txt")
			if a.Size != 2 || a.ContentType != "application/x-custom" {
				t.Fatalf("overwrite = %+v", a)
			}

			if err := blob.PutBytes(ctx, b, "docs2/b.txt", []byte("b"), nil); err != nil {
				t.Fatal(err)
			}
			if err := blob.PutBytes(ctx, b, "img/c.png", []byte("c"), nil); err != nil {
				t.Fatal(err)
			}
			if got := listKeys(t, b, ""); !slices.Equal(got, []string{"docs/a.txt", "docs/page", "docs2/b.txt", "img/c.png"}) {
				t.Fatalf("list all = %v", got)
			}
			if got := listKeys(t, b, "docs"); !slices.Equal(got, []string{"docs/a.txt", "docs/page", "docs2/b.txt"}) {
				t.Fatalf("list docs = %v", got)
			}
			if got := listKeys(t, b, "docs/"); !slices.Equal(got, []string{"docs/a.txt", "docs/page"}) {
				t.Fatalf("list docs/ = %v", got)
			}
			if got := listKeys(t, b, "docs/p"); !slices.Equal(got, []string{"docs/page"}) {
				t.Fatalf("list docs/p = %v", got)
			}
			if got := listKeys(t, b, "nope/"); len(got) != 0 {
				t.Fatalf("list nope = %v", got)
			}

			// 提前 break 不应 panic
			for range b.List(ctx, "") {
				break
			}

			if err := blob.Copy(ctx, b, "docs/page", "copy/page"); err != nil {
				t.Fatal(err)
			}
			if a, _ := b.Stat(ctx, "copy/page"); a.Metadata["owner"] != "u1" {
				t.Fatalf("copy 未保留 metadata: %+v", a)
			}

			if err := b.Delete(ctx, "docs/a.txt"); err != nil {
				t.Fatal(err)
			}
			if err := b.Delete(ctx, "docs/a.txt"); err != nil {
				t.Fatalf("重复删除应幂等: %v", err)
			}
			if ok, _ := blob.Exists(ctx, b, "docs/a.txt"); ok {
				t.Fatal("删除后仍存在")
			}
			n, err := blob.DeletePrefix(ctx, b, "docs")
			if err != nil || n != 2 {
				t.Fatalf("DeletePrefix = %d, %v", n, err)
			}
			if _, err := blob.DeletePrefix(ctx, b, ""); err == nil {
				t.Fatal("空前缀应拒绝")
			}
		})
	}
}

func TestBucket_InvalidKeys(t *testing.T) {
	ctx := context.Background()
	bad := []string{"", ".", "/abs", "a/../b", "../escape", "a//b", "trailing/", "./a"}
	for name, b := range buckets(t) {
		for _, k := range bad {
			if err := blob.PutBytes(ctx, b, k, []byte("x"), nil); !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("%s put %q: %v", name, k, err)
			}
			if _, err := b.Stat(ctx, k); !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("%s stat %q: %v", name, k, err)
			}
		}
	}
}

func TestLocal_Layout(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b, err := blob.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := blob.PutBytes(ctx, b, ".blob/attrs/x", []byte("x"), nil); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("保留目录应拒绝: %v", err)
	}
	if err := blob.PutBytes(ctx, b, "a/b/c.txt", []byte("x"), nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt")); err != nil || string(data) != "x" {
		t.Fatalf("文件应按 key 落盘: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".blob", "attrs", "a", "b", "c.txt.json")); !os.IsNotExist(err) {
		t.Fatalf("类型可由扩展名推断时不应写旁路文件: %v", err)
	}
	if err := b.Delete(ctx, "a/b/c.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("空父目录应被清理: %v", err)
	}

	// 指向根外的符号链接不能被读出
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skip("symlink unsupported:", err)
	}
	if _, err := blob.GetBytes(ctx, b, "link.txt"); err == nil {
		t.Fatal("逃逸根目录的符号链接不应可读")
	}
}

func TestLocal_ConcurrentPut(t *testing.T) {
	ctx := context.Background()
	b, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := blob.PutBytes(ctx, b, "same/key.txt", []byte(strings.Repeat("x", i+1)), nil); err != nil {
				t.Error(err)
			}
			_ = b.Delete(ctx, "other/k.txt")
		}()
	}
	wg.Wait()
	data, err := blob.GetBytes(ctx, b, "same/key.txt")
	if err != nil || len(data) == 0 || strings.Trim(string(data), "x") != "" {
		t.Fatalf("data = %q, %v", data, err)
	}
}

func TestServe(t *testing.T) {
	ctx := context.Background()
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			if err := blob.PutBytes(ctx, b, "f/hello.txt", []byte("hello world"), nil); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				blob.Serve(w, r, b, strings.TrimPrefix(r.URL.Path, "/"))
			}))
			defer srv.Close()

			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/f/hello.txt", nil)
			req.Header.Set("Range", "bytes=0-4")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusPartialContent || string(body) != "hello" {
				t.Fatalf("range: %d %q", resp.StatusCode, body)
			}
			if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("缺少 nosniff")
			}

			resp, err = http.Get(srv.URL + "/f/missing.txt")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("missing: %d", resp.StatusCode)
			}
		})
	}
}

func TestPut_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, b := range buckets(t) {
		if err := blob.PutBytes(ctx, b, "x.txt", []byte("x"), nil); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", name, err)
		}
		if ok, _ := blob.Exists(context.Background(), b, "x.txt"); ok {
			t.Errorf("%s: 取消的写入不应留下对象", name)
		}
	}
}
