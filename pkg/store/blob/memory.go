package blob

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"iter"
	"slices"
	"strings"
	"sync"
	"time"
)

var _ Bucket = (*Memory)(nil)

// Memory 是内存 Bucket,用于测试与单机原型。List 按 key 字典序返回。并发安全。
type Memory struct {
	mu      sync.RWMutex
	objects map[string]memObject
}

type memObject struct {
	data  []byte
	attrs Attributes
}

// NewMemory 创建空的内存 Bucket。
func NewMemory() *Memory {
	return &Memory{objects: make(map[string]memObject)}
}

func (m *Memory) Put(ctx context.Context, key string, r io.Reader, opts *PutOptions) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	var o PutOptions
	if opts != nil {
		o = *opts
	}
	br := bufio.NewReader(ctxReader{ctx: ctx, r: r})
	if o.ContentType == "" {
		o.ContentType = detectContentType(key, br)
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return err
	}
	obj := memObject{data: data, attrs: Attributes{
		Key:         key,
		Size:        int64(len(data)),
		ContentType: o.ContentType,
		ModTime:     time.Now(),
		Metadata:    cloneMeta(o.Metadata),
	}}
	m.mu.Lock()
	m.objects[key] = obj
	m.mu.Unlock()
	return nil
}

func (m *Memory) Get(ctx context.Context, key string) (io.ReadCloser, Attributes, error) {
	obj, err := m.lookup(ctx, key)
	if err != nil {
		return nil, Attributes{}, err
	}
	return readSeekNopCloser{bytes.NewReader(obj.data)}, copyAttrs(obj.attrs), nil
}

func (m *Memory) Stat(ctx context.Context, key string) (Attributes, error) {
	obj, err := m.lookup(ctx, key)
	if err != nil {
		return Attributes{}, err
	}
	return copyAttrs(obj.attrs), nil
}

func (m *Memory) lookup(ctx context.Context, key string) (memObject, error) {
	if err := ValidateKey(key); err != nil {
		return memObject{}, err
	}
	if err := ctx.Err(); err != nil {
		return memObject{}, err
	}
	m.mu.RLock()
	obj, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return memObject{}, ErrNotFound
	}
	return obj, nil
}

func (m *Memory) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

// List 先在锁内拍快照再逐个 yield,遍历期间的写入不影响本次结果。
func (m *Memory) List(ctx context.Context, prefix string) iter.Seq2[Attributes, error] {
	return func(yield func(Attributes, error) bool) {
		m.mu.RLock()
		items := make([]Attributes, 0, len(m.objects))
		for k, obj := range m.objects {
			if strings.HasPrefix(k, prefix) {
				items = append(items, copyAttrs(obj.attrs))
			}
		}
		m.mu.RUnlock()
		slices.SortFunc(items, func(a, b Attributes) int { return strings.Compare(a.Key, b.Key) })
		for _, a := range items {
			if err := ctx.Err(); err != nil {
				yield(Attributes{}, err)
				return
			}
			if !yield(a, nil) {
				return
			}
		}
	}
}

func (m *Memory) Close() error { return nil }

func copyAttrs(a Attributes) Attributes {
	a.Metadata = cloneMeta(a.Metadata)
	return a
}

type readSeekNopCloser struct{ *bytes.Reader }

func (readSeekNopCloser) Close() error { return nil }
