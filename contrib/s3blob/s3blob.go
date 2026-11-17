// Package s3blob 用 minio-go 实现 pkg/store/blob.Bucket,兼容 AWS S3 及各类 S3 协议存储
// (MinIO、Cloudflare R2、阿里云 OSS、腾讯云 COS 等)。业务代码只依赖 blob.Bucket,
// 本地开发用 blob.NewLocal,上线换成 s3blob.New 即可。
//
// 约定与差异:
//   - Config.Prefix 非空时所有 key 落在该前缀下(多个服务共用一个桶),对调用方透明;
//   - Metadata 走 S3 的 x-amz-meta-*,键名大小写不敏感,读回统一小写;
//   - List 返回的 Attributes 不含 ContentType 与 Metadata(S3 列举接口不返回),需要时再 Stat;
//   - Get 返回的 reader 支持 Seek,blob.Serve 可直接提供 Range 下载。
package s3blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/rushteam/beauty/pkg/store/blob"
)

var _ blob.Bucket = (*Bucket)(nil)

// Config 是 S3 连接配置。
type Config struct {
	// Endpoint 不带协议,如 "s3.amazonaws.com"、"localhost:9000"、"<account>.r2.cloudflarestorage.com"。
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	// UseSSL 为 true 时走 HTTPS。
	UseSSL bool
	// PathStyle 强制路径风格寻址(endpoint/bucket/key),MinIO 等自建服务常需开启。
	PathStyle bool
	// Prefix 是桶内 key 前缀(如 "svc-a/"),须为合法 key 片段;为空表示不加前缀。
	Prefix string
}

// Bucket 是基于 S3 的 blob.Bucket,并发安全。
type Bucket struct {
	client *minio.Client
	bucket string
	prefix string
}

// New 按配置创建 Bucket。不会发起网络请求,桶需事先存在。
func New(cfg Config) (*Bucket, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("s3blob: endpoint and bucket are required")
	}
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(cfg.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("s3blob: %w", err)
	}
	return NewFromClient(client, cfg.Bucket, cfg.Prefix)
}

// NewFromClient 复用已有的 minio.Client(自定义 Transport、凭证链等)。
func NewFromClient(client *minio.Client, bucket, prefix string) (*Bucket, error) {
	if prefix != "" {
		trimmed := strings.TrimSuffix(prefix, "/")
		if err := blob.ValidateKey(trimmed); err != nil {
			return nil, fmt.Errorf("s3blob: invalid prefix: %w", err)
		}
		prefix = trimmed + "/"
	}
	return &Bucket{client: client, bucket: bucket, prefix: prefix}, nil
}

// Client 返回底层 minio.Client,用于本接口未覆盖的操作(生命周期规则、分享策略等)。
func (b *Bucket) Client() *minio.Client { return b.client }

func (b *Bucket) object(key string) (string, error) {
	if err := blob.ValidateKey(key); err != nil {
		return "", err
	}
	return b.prefix + key, nil
}

// Put 未知长度的 reader 会走分片上传(minio-go 按分片在内存中缓冲)。
func (b *Bucket) Put(ctx context.Context, key string, r io.Reader, opts *blob.PutOptions) error {
	name, err := b.object(key)
	if err != nil {
		return err
	}
	var o blob.PutOptions
	if opts != nil {
		o = *opts
	}
	// 长度要在包装嗅探 reader 之前取:bytes.Reader / strings.Reader / bytes.Buffer 的 Len 是剩余字节数。
	size := int64(-1)
	if l, ok := r.(interface{ Len() int }); ok {
		size = int64(l.Len())
	}
	if o.ContentType == "" {
		o.ContentType, r = blob.DetectContentType(key, r)
	}
	_, err = b.client.PutObject(ctx, b.bucket, name, r, size, minio.PutObjectOptions{
		ContentType:  o.ContentType,
		UserMetadata: o.Metadata,
	})
	return mapErr(err)
}

func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, blob.Attributes, error) {
	name, err := b.object(key)
	if err != nil {
		return nil, blob.Attributes{}, err
	}
	obj, err := b.client.GetObject(ctx, b.bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, blob.Attributes{}, mapErr(err)
	}
	// GetObject 是惰性的,Stat 才真正发请求并确认对象存在。
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, blob.Attributes{}, mapErr(err)
	}
	return obj, b.attrs(info, key), nil
}

func (b *Bucket) Stat(ctx context.Context, key string) (blob.Attributes, error) {
	name, err := b.object(key)
	if err != nil {
		return blob.Attributes{}, err
	}
	info, err := b.client.StatObject(ctx, b.bucket, name, minio.StatObjectOptions{})
	if err != nil {
		return blob.Attributes{}, mapErr(err)
	}
	return b.attrs(info, key), nil
}

func (b *Bucket) Delete(ctx context.Context, key string) error {
	name, err := b.object(key)
	if err != nil {
		return err
	}
	return mapErr(b.client.RemoveObject(ctx, b.bucket, name, minio.RemoveObjectOptions{}))
}

func (b *Bucket) List(ctx context.Context, prefix string) iter.Seq2[blob.Attributes, error] {
	return func(yield func(blob.Attributes, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel() // 提前结束遍历时停止 minio-go 的后台分页
		for info := range b.client.ListObjects(ctx, b.bucket, minio.ListObjectsOptions{
			Prefix:    b.prefix + prefix,
			Recursive: true,
		}) {
			if info.Err != nil {
				yield(blob.Attributes{}, mapErr(info.Err))
				return
			}
			if strings.HasSuffix(info.Key, "/") {
				continue // 控制台创建的"目录"占位对象
			}
			a := blob.Attributes{
				Key:     strings.TrimPrefix(info.Key, b.prefix),
				Size:    info.Size,
				ModTime: info.LastModified,
			}
			if !yield(a, nil) {
				return
			}
		}
	}
}

// PresignedGetURL 生成限时直链,让客户端绕过服务直接从对象存储下载。
func (b *Bucket) PresignedGetURL(ctx context.Context, key string, expires time.Duration) (*url.URL, error) {
	name, err := b.object(key)
	if err != nil {
		return nil, err
	}
	u, err := b.client.PresignedGetObject(ctx, b.bucket, name, expires, nil)
	return u, mapErr(err)
}

// Close 无需释放资源(底层 HTTP 连接由 Transport 管理)。
func (b *Bucket) Close() error { return nil }

func (b *Bucket) attrs(info minio.ObjectInfo, key string) blob.Attributes {
	a := blob.Attributes{
		Key:         key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ModTime:     info.LastModified,
	}
	if len(info.UserMetadata) > 0 {
		a.Metadata = make(map[string]string, len(info.UserMetadata))
		for k, v := range info.UserMetadata {
			a.Metadata[strings.ToLower(k)] = v
		}
	}
	return a
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.StatusCode == http.StatusNotFound && resp.Code != "NoSuchBucket" {
		return blob.ErrNotFound
	}
	switch resp.Code {
	case "NoSuchKey", "NotFound":
		return blob.ErrNotFound
	}
	return err
}
