// Package blob 定义对象(文件)存储的最小接口 Bucket,并提供本地磁盘(Local)与内存(Memory)
// 两种实现。S3/OSS/R2 等云存储由 contrib 模块按同一接口实现,业务代码只依赖 Bucket。
//
// 约定:
//   - key 用 "/" 分隔的相对路径(如 "avatars/u1.png"),必须满足 fs.ValidPath:不能为空、
//     不能以 "/" 开头或结尾、不能含 "."、".." 或空段。ValidateKey 统一校验,杜绝路径穿越;
//   - Put 是整对象覆盖写,写入对读者原子可见(Local 先写临时文件再 rename);
//   - Delete 幂等,对象不存在不报错;Stat/Get 不存在返回 ErrNotFound;
//   - List 按 key 的字符串前缀过滤(S3 语义,不是目录语义),顺序取决于实现。
//
// 本包只管"字节怎么存取",不管鉴权、配额、缩略图等上层策略。Bucket 实现须并发安全。
package blob

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"iter"
	"mime"
	"net/http"
	"path"
	"strconv"
	"time"
)

var (
	// ErrNotFound 对象不存在。
	ErrNotFound = errors.New("blob: not found")
	// ErrInvalidKey key 不合法(见 ValidateKey)。
	ErrInvalidKey = errors.New("blob: invalid key")
)

// Attributes 是对象的元信息。
type Attributes struct {
	Key         string
	Size        int64
	ContentType string
	ModTime     time.Time
	Metadata    map[string]string // 自定义元数据(实现可能对 key/value 字符集有限制)
}

// PutOptions 是 Put 的可选参数,nil 等价于零值。
type PutOptions struct {
	// ContentType 为空时按扩展名推断,推断不出再嗅探内容前 512 字节。
	ContentType string
	Metadata    map[string]string
}

// Bucket 是对象存储。所有方法并发安全。
type Bucket interface {
	// Put 写入(覆盖)key 对应的对象,读取 r 直到 EOF。
	Put(ctx context.Context, key string, r io.Reader, opts *PutOptions) error
	// Get 打开对象读取,调用方负责 Close。返回的 reader 若实现 io.ReadSeeker,Serve 会支持 Range。
	Get(ctx context.Context, key string) (io.ReadCloser, Attributes, error)
	// Stat 返回对象元信息。
	Stat(ctx context.Context, key string) (Attributes, error)
	// Delete 删除对象,不存在不报错。
	Delete(ctx context.Context, key string) error
	// List 按前缀遍历对象,顺序取决于实现。prefix 为空遍历全部。出错时 yield 一次非 nil error 后结束。
	List(ctx context.Context, prefix string) iter.Seq2[Attributes, error]
	// Close 释放底层资源。
	Close() error
}

// ValidateKey 校验 key 是否合法,不合法返回包装了 ErrInvalidKey 的错误。
func ValidateKey(key string) error {
	if key == "." || !fs.ValidPath(key) {
		return &keyError{key: key}
	}
	return nil
}

type keyError struct{ key string }

func (e *keyError) Error() string { return "blob: invalid key " + strconv.Quote(e.key) }
func (e *keyError) Unwrap() error { return ErrInvalidKey }

// PutBytes 是 Put 的 []byte 便捷版。
func PutBytes(ctx context.Context, b Bucket, key string, data []byte, opts *PutOptions) error {
	return b.Put(ctx, key, bytes.NewReader(data), opts)
}

// GetBytes 读出整个对象。大对象请直接用 Get 流式读取。
func GetBytes(ctx context.Context, b Bucket, key string) ([]byte, error) {
	rc, _, err := b.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Exists 报告对象是否存在。
func Exists(ctx context.Context, b Bucket, key string) (bool, error) {
	_, err := b.Stat(ctx, key)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// Copy 把 src 复制到 dst(同一个 Bucket),保留 ContentType 与 Metadata。
func Copy(ctx context.Context, b Bucket, src, dst string) error {
	rc, attrs, err := b.Get(ctx, src)
	if err != nil {
		return err
	}
	defer rc.Close()
	return b.Put(ctx, dst, rc, &PutOptions{ContentType: attrs.ContentType, Metadata: attrs.Metadata})
}

// DeletePrefix 删除所有以 prefix 开头的对象,返回删除数量。prefix 为空时拒绝执行,防止误删整个桶。
func DeletePrefix(ctx context.Context, b Bucket, prefix string) (int, error) {
	if prefix == "" {
		return 0, errors.New("blob: DeletePrefix requires non-empty prefix")
	}
	var keys []string
	for a, err := range b.List(ctx, prefix) {
		if err != nil {
			return 0, err
		}
		keys = append(keys, a.Key)
	}
	for i, k := range keys {
		if err := b.Delete(ctx, k); err != nil {
			return i, err
		}
	}
	return len(keys), nil
}

// Serve 把对象写到 HTTP 响应:设置 Content-Type / Last-Modified,支持条件请求;
// reader 可 Seek 时还支持 Range。对象不存在回 404,key 非法回 400。
// 总是附带 X-Content-Type-Options: nosniff;用户上传的 HTML 等内容是否允许内联展示由调用方决定
// (必要时自行设置 Content-Disposition: attachment)。
func Serve(w http.ResponseWriter, r *http.Request, b Bucket, key string) {
	rc, attrs, err := b.Get(r.Context(), key)
	switch {
	case errors.Is(err, ErrInvalidKey):
		http.Error(w, "invalid key", http.StatusBadRequest)
		return
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if attrs.ContentType != "" {
		h.Set("Content-Type", attrs.ContentType)
	}
	if rs, ok := rc.(io.ReadSeeker); ok {
		http.ServeContent(w, r, path.Base(key), attrs.ModTime, rs)
		return
	}
	if !attrs.ModTime.IsZero() {
		h.Set("Last-Modified", attrs.ModTime.UTC().Format(http.TimeFormat))
	}
	h.Set("Content-Length", strconv.FormatInt(attrs.Size, 10))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, rc)
}

// DetectContentType 供 Bucket 实现在 PutOptions.ContentType 为空时推断类型:先看 key 的扩展名,
// 推断不出再嗅探 r 的前 512 字节。返回的 reader 包含被嗅探的数据,调用方应改读它。
func DetectContentType(key string, r io.Reader) (string, io.Reader) {
	br := bufio.NewReader(r)
	return detectContentType(key, br), br
}

func detectContentType(key string, br *bufio.Reader) string {
	if ct := mime.TypeByExtension(path.Ext(key)); ct != "" {
		return ct
	}
	head, _ := br.Peek(512)
	return http.DetectContentType(head)
}

// ctxReader 在每次 Read 前检查 ctx,让大对象拷贝能被取消。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func cloneMeta(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
