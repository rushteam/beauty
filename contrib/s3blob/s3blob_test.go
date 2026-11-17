package s3blob_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/minio/minio-go/v7"

	"github.com/rushteam/beauty/contrib/s3blob"
	"github.com/rushteam/beauty/pkg/store/blob"
)

func fakeS3(t *testing.T, bucket string) string {
	t.Helper()
	backend := s3mem.New()
	if err := backend.CreateBucket(bucket); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Host
}

func newBucket(t *testing.T, endpoint, prefix string) *s3blob.Bucket {
	t.Helper()
	b, err := s3blob.New(s3blob.Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: "test",
		AccessKey: "ak", SecretKey: "sk", PathStyle: true, Prefix: prefix,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func keys(t *testing.T, b blob.Bucket, prefix string) []string {
	t.Helper()
	var out []string
	for a, err := range b.List(context.Background(), prefix) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a.Key)
	}
	slices.Sort(out)
	return out
}

func TestBucket(t *testing.T) {
	ctx := context.Background()
	endpoint := fakeS3(t, "test")
	b := newBucket(t, endpoint, "svc-a")

	if _, err := b.Stat(ctx, "missing.txt"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("stat missing: %v", err)
	}
	if _, _, err := b.Get(ctx, "missing.txt"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	if err := blob.PutBytes(ctx, b, "../x", []byte("x"), nil); !errors.Is(err, blob.ErrInvalidKey) {
		t.Fatalf("invalid key: %v", err)
	}

	if err := blob.PutBytes(ctx, b, "docs/a.txt", []byte("hello world"), &blob.PutOptions{Metadata: map[string]string{"Owner": "u1"}}); err != nil {
		t.Fatal(err)
	}
	a, err := b.Stat(ctx, "docs/a.txt")
	if err != nil || a.Size != 11 || !strings.HasPrefix(a.ContentType, "text/plain") || a.Metadata["owner"] != "u1" {
		t.Fatalf("stat = %+v, %v", a, err)
	}
	// 无扩展名、未知长度的 reader:嗅探 + 分片上传
	if err := b.Put(ctx, "docs/page", io.MultiReader(strings.NewReader("<html><body>"), strings.NewReader("x</body></html>")), nil); err != nil {
		t.Fatal(err)
	}
	if a, _ := b.Stat(ctx, "docs/page"); !strings.HasPrefix(a.ContentType, "text/html") {
		t.Fatalf("sniff = %+v", a)
	}
	if err := blob.PutBytes(ctx, b, "img/c.png", []byte("c"), nil); err != nil {
		t.Fatal(err)
	}

	if got := keys(t, b, ""); !slices.Equal(got, []string{"docs/a.txt", "docs/page", "img/c.png"}) {
		t.Fatalf("list = %v", got)
	}
	if got := keys(t, b, "docs/"); !slices.Equal(got, []string{"docs/a.txt", "docs/page"}) {
		t.Fatalf("list docs/ = %v", got)
	}
	for range b.List(ctx, "") {
		break
	}

	// 前缀隔离:另一个前缀看不到 svc-a 的对象
	other := newBucket(t, endpoint, "svc-b/")
	if got := keys(t, other, ""); len(got) != 0 {
		t.Fatalf("前缀未隔离: %v", got)
	}

	rc, attrs, err := b.Get(ctx, "docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.(io.ReadSeeker); !ok {
		t.Fatal("Get 应返回可 Seek 的 reader")
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello world" || attrs.Key != "docs/a.txt" {
		t.Fatalf("get = %q %+v", data, attrs)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blob.Serve(w, r, b, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/docs/a.txt", nil)
	req.Header.Set("Range", "bytes=6-10")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "world" {
		t.Fatalf("range = %d %q", resp.StatusCode, body)
	}

	u, err := b.PresignedGetURL(ctx, "docs/a.txt", time.Minute)
	if err != nil || !strings.Contains(u.Path, "svc-a/docs/a.txt") || u.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("presigned = %v, %v", u, err)
	}

	if err := blob.Copy(ctx, b, "docs/a.txt", "copy/a.txt"); err != nil {
		t.Fatal(err)
	}
	if n, err := blob.DeletePrefix(ctx, b, "docs/"); err != nil || n != 2 {
		t.Fatalf("DeletePrefix = %d, %v", n, err)
	}
	if err := b.Delete(ctx, "docs/a.txt"); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
	if got := keys(t, b, ""); !slices.Equal(got, []string{"copy/a.txt", "img/c.png"}) {
		t.Fatalf("after delete = %v", got)
	}
}

func TestNew_Validation(t *testing.T) {
	if _, err := s3blob.New(s3blob.Config{}); err == nil {
		t.Fatal("缺少 endpoint/bucket 应报错")
	}
	client, _ := minio.New("localhost:9000", &minio.Options{})
	if _, err := s3blob.NewFromClient(client, "b", "../bad"); err == nil {
		t.Fatal("非法前缀应报错")
	}
}
