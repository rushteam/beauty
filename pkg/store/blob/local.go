package blob

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// Local 内部使用的保留目录:attrs 存 ContentType/Metadata 旁路文件,tmp 存写入中的临时文件。
// 以该目录开头的 key 会被拒绝,List 也会跳过它。
const (
	localReservedDir = ".blob"
	localAttrsDir    = ".blob/attrs"
	localTmpDir      = ".blob/tmp"
)

var _ Bucket = (*Local)(nil)

// Local 是基于本地目录的 Bucket。所有文件操作都经由 os.Root,符号链接等也无法逃出根目录。
//
// ContentType 与扩展名推断一致且无 Metadata 时不落旁路文件;否则在 .blob/attrs 下写一份 JSON。
// 数据与旁路文件分两步替换,并发覆盖同一 key 时元信息可能短暂与数据不一致。
type Local struct {
	root *os.Root
}

type localSidecar struct {
	ContentType string            `json:"contentType,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// NewLocal 以 dir 为根目录创建 Local,dir 不存在会自动创建。
func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("blob: create dir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("blob: open root: %w", err)
	}
	return &Local{root: root}, nil
}

func (l *Local) checkKey(key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if key == localReservedDir || strings.HasPrefix(key, localReservedDir+"/") {
		return &keyError{key: key}
	}
	return nil
}

// Put 先写入 .blob/tmp 下的临时文件,再 rename 到目标位置,读者不会看到写了一半的对象。
func (l *Local) Put(ctx context.Context, key string, r io.Reader, opts *PutOptions) error {
	if err := l.checkKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var o PutOptions
	if opts != nil {
		o = *opts
	}

	tmp, err := l.writeTemp(ctx, key, r, &o)
	if err != nil {
		return err
	}
	if err := l.renameInto(tmp, key); err != nil {
		_ = l.root.Remove(fp(tmp))
		return err
	}

	if o.ContentType != mime.TypeByExtension(path.Ext(key)) || len(o.Metadata) > 0 {
		data, err := json.Marshal(localSidecar{ContentType: o.ContentType, Metadata: o.Metadata})
		if err != nil {
			return err
		}
		return l.writeFileAtomic(sidecarKey(key), data)
	}
	if err := l.root.Remove(fp(sidecarKey(key))); err != nil && !isNotExist(err) {
		return err
	}
	return nil
}

// writeTemp 把 r 写到临时文件,并在 o.ContentType 为空时就地推断。返回临时文件 key。
func (l *Local) writeTemp(ctx context.Context, key string, r io.Reader, o *PutOptions) (string, error) {
	if err := l.root.MkdirAll(fp(localTmpDir), 0o755); err != nil {
		return "", err
	}
	tmp := path.Join(localTmpDir, randomName())
	f, err := l.root.OpenFile(fp(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	br := bufio.NewReader(ctxReader{ctx: ctx, r: r})
	if o.ContentType == "" {
		o.ContentType = detectContentType(key, br)
	}
	_, err = io.Copy(f, br)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = l.root.Remove(fp(tmp))
		return "", err
	}
	return tmp, nil
}

// renameInto 把临时文件挪到 dst。并发 Delete 可能刚好清掉了空的父目录,遇到 ENOENT 时重建父目录再试。
func (l *Local) renameInto(tmp, dst string) error {
	var err error
	for range 3 {
		if dir := path.Dir(dst); dir != "." {
			if err = l.root.MkdirAll(fp(dir), 0o755); err != nil {
				return err
			}
		}
		if err = l.root.Rename(fp(tmp), fp(dst)); err == nil || !isNotExist(err) {
			return err
		}
	}
	return err
}

func (l *Local) writeFileAtomic(key string, data []byte) error {
	if err := l.root.MkdirAll(fp(localTmpDir), 0o755); err != nil {
		return err
	}
	tmp := path.Join(localTmpDir, randomName())
	if err := l.root.WriteFile(fp(tmp), data, 0o644); err != nil {
		return err
	}
	if err := l.renameInto(tmp, key); err != nil {
		_ = l.root.Remove(fp(tmp))
		return err
	}
	return nil
}

func (l *Local) Get(ctx context.Context, key string) (io.ReadCloser, Attributes, error) {
	if err := l.checkKey(key); err != nil {
		return nil, Attributes{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, Attributes{}, err
	}
	f, err := l.root.Open(fp(key))
	if err != nil {
		return nil, Attributes{}, mapNotExist(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Attributes{}, err
	}
	if fi.IsDir() {
		f.Close()
		return nil, Attributes{}, ErrNotFound
	}
	return f, l.attrs(key, fi), nil
}

func (l *Local) Stat(ctx context.Context, key string) (Attributes, error) {
	if err := l.checkKey(key); err != nil {
		return Attributes{}, err
	}
	if err := ctx.Err(); err != nil {
		return Attributes{}, err
	}
	fi, err := l.root.Stat(fp(key))
	if err != nil {
		return Attributes{}, mapNotExist(err)
	}
	if fi.IsDir() {
		return Attributes{}, ErrNotFound
	}
	return l.attrs(key, fi), nil
}

// Delete 删除对象及其旁路文件,并尽力清理因此变空的父目录。
func (l *Local) Delete(ctx context.Context, key string) error {
	if err := l.checkKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := l.root.Lstat(fp(key))
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return nil
	}
	if err := l.root.Remove(fp(key)); err != nil && !isNotExist(err) {
		return err
	}
	sc := sidecarKey(key)
	if err := l.root.Remove(fp(sc)); err != nil && !isNotExist(err) {
		return err
	}
	l.pruneDirs(path.Dir(key), ".")
	l.pruneDirs(path.Dir(sc), localAttrsDir)
	return nil
}

// pruneDirs 自 dir 向上删除空目录,直到 stop(不含)或遇到非空目录。
func (l *Local) pruneDirs(dir, stop string) {
	for ; dir != stop && dir != "."; dir = path.Dir(dir) {
		if l.root.Remove(fp(dir)) != nil {
			return
		}
	}
}

// List 深度优先遍历目录,按前缀剪枝,不进入不可能匹配的子目录。
func (l *Local) List(ctx context.Context, prefix string) iter.Seq2[Attributes, error] {
	return func(yield func(Attributes, error) bool) {
		start := "."
		if i := strings.LastIndex(prefix, "/"); i >= 0 {
			start = prefix[:i]
			if !fs.ValidPath(start) {
				yield(Attributes{}, &keyError{key: prefix})
				return
			}
		}
		stopped := false
		err := fs.WalkDir(l.root.FS(), start, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == start && isNotExist(err) {
					return fs.SkipAll
				}
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				if p == localReservedDir {
					return fs.SkipDir
				}
				if p != "." && !strings.HasPrefix(p+"/", prefix) && !strings.HasPrefix(prefix, p+"/") {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasPrefix(p, prefix) {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				if isNotExist(err) {
					return nil // 遍历期间被并发删除
				}
				return err
			}
			if !yield(l.attrs(p, fi), nil) {
				stopped = true
				return fs.SkipAll
			}
			return nil
		})
		if err != nil && !stopped {
			yield(Attributes{}, err)
		}
	}
}

func (l *Local) Close() error {
	return l.root.Close()
}

func (l *Local) attrs(key string, fi fs.FileInfo) Attributes {
	a := Attributes{Key: key, Size: fi.Size(), ModTime: fi.ModTime()}
	if data, err := l.root.ReadFile(fp(sidecarKey(key))); err == nil {
		var sc localSidecar
		if json.Unmarshal(data, &sc) == nil {
			a.ContentType = sc.ContentType
			a.Metadata = sc.Metadata
		}
	}
	if a.ContentType == "" {
		a.ContentType = mime.TypeByExtension(path.Ext(key))
	}
	if a.ContentType == "" {
		a.ContentType = "application/octet-stream"
	}
	return a
}

func sidecarKey(key string) string {
	return localAttrsDir + "/" + key + ".json"
}

func fp(key string) string {
	return filepath.FromSlash(key)
}

func randomName() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// isNotExist 把"路径中间某段是文件"(ENOTDIR)也视为不存在。
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func mapNotExist(err error) error {
	if isNotExist(err) {
		return ErrNotFound
	}
	return err
}
