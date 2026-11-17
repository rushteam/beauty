package jsvm

import (
	"bytes"
	"errors"

	"github.com/dop251/goja"

	"github.com/rushteam/beauty/pkg/store/blob"
)

// bindFilesystem 绑定 $filesystem(需 WithBucket),对象元信息形如
// {key, size, contentType, modTime(毫秒时间戳), metadata}。不存在的对象:get 抛异常,stat 返回 null。
func (inst *instance) bindFilesystem() error {
	rt := inst.rt
	bucket := func() blob.Bucket {
		b := inst.vm.cfg.bucket
		if b == nil {
			inst.throw(errNotConfigured("$filesystem", "jsvm.WithBucket"))
		}
		return b
	}
	attrs := func(a blob.Attributes) *goja.Object {
		o := rt.NewObject()
		meta := rt.NewObject()
		for k, v := range a.Metadata {
			_ = meta.Set(k, v)
		}
		_ = o.Set("key", a.Key)
		_ = o.Set("size", a.Size)
		_ = o.Set("contentType", a.ContentType)
		_ = o.Set("modTime", a.ModTime.UnixMilli())
		_ = o.Set("metadata", meta)
		return o
	}
	get := func(key string) []byte {
		data, err := blob.GetBytes(inst.ctx, bucket(), key)
		if err != nil {
			inst.throw(err)
		}
		return data
	}

	obj := rt.NewObject()
	if err := setAll(obj, map[string]any{
		// put(key, data, {contentType, metadata}?)
		"put": func(key string, data goja.Value, opts *goja.Object) {
			b, ok := inst.toBytes(data)
			if !ok {
				panic(rt.NewTypeError("$filesystem.put: data must be a string or binary"))
			}
			var po blob.PutOptions
			if opts != nil {
				if ct := opts.Get("contentType"); isSet(ct) {
					po.ContentType = ct.String()
				}
				if m, ok := opts.Get("metadata").(*goja.Object); ok {
					po.Metadata = map[string]string{}
					for _, k := range m.Keys() {
						po.Metadata[k] = m.Get(k).String()
					}
				}
			}
			if err := bucket().Put(inst.ctx, key, bytes.NewReader(b), &po); err != nil {
				inst.throw(err)
			}
		},
		"get":       func(key string) goja.ArrayBuffer { return rt.NewArrayBuffer(get(key)) },
		"getString": func(key string) string { return string(get(key)) },
		"stat": func(key string) goja.Value {
			a, err := bucket().Stat(inst.ctx, key)
			if errors.Is(err, blob.ErrNotFound) {
				return goja.Null()
			}
			if err != nil {
				inst.throw(err)
			}
			return attrs(a)
		},
		"exists": func(key string) bool {
			ok, err := blob.Exists(inst.ctx, bucket(), key)
			if err != nil {
				inst.throw(err)
			}
			return ok
		},
		"delete": func(key string) {
			if err := bucket().Delete(inst.ctx, key); err != nil {
				inst.throw(err)
			}
		},
		"copy": func(src, dst string) {
			if err := blob.Copy(inst.ctx, bucket(), src, dst); err != nil {
				inst.throw(err)
			}
		},
		"list": func(prefix string) []any {
			var out []any
			for a, err := range bucket().List(inst.ctx, prefix) {
				if err != nil {
					inst.throw(err)
				}
				out = append(out, attrs(a))
			}
			return out
		},
	}); err != nil {
		return err
	}
	return rt.Set("$filesystem", obj)
}
