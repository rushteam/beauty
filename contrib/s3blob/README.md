# contrib/s3blob —— blob.Bucket 的 S3 实现(独立模块)

用 minio-go 实现 `pkg/store/blob.Bucket`,兼容 AWS S3 与各类 S3 协议存储(MinIO、Cloudflare R2、
阿里云 OSS、腾讯云 COS…)。业务只依赖 `blob.Bucket`:本地开发用 `blob.NewLocal`,上线换成 `s3blob.New`。

```bash
go get github.com/rushteam/beauty/contrib/s3blob@latest
```

```go
bucket, err := s3blob.New(s3blob.Config{
    Endpoint:  "s3.amazonaws.com", // MinIO: "minio:9000" + PathStyle: true
    Region:    "us-east-1",
    Bucket:    "my-bucket",
    AccessKey: os.Getenv("S3_AK"),
    SecretKey: os.Getenv("S3_SK"),
    UseSSL:    true,
    Prefix:    "svc-a/", // 可选:多个服务共用一个桶时按前缀隔离
})

_ = blob.PutBytes(ctx, bucket, "avatars/u1.png", data, nil) // ContentType 按扩展名/内容推断
blob.Serve(w, r, bucket, "avatars/u1.png")                   // 支持 Range / 条件请求
u, _ := bucket.PresignedGetURL(ctx, "avatars/u1.png", 10*time.Minute) // 客户端直连下载

vm, _ := jsvm.New(jsvm.WithDir("hooks"), jsvm.WithBucket(bucket)) // 脚本里 $filesystem 直接可用
```

注意:

- Metadata 走 `x-amz-meta-*`,键名大小写不敏感,读回统一小写;
- `List` 结果不含 ContentType / Metadata(S3 列举接口不返回),需要时再 `Stat`;
- 长度未知的 reader 走分片上传,minio-go 会按分片在内存中缓冲;已知长度(`bytes.Reader` 等)直接单次上传;
- 桶需事先创建;生命周期、跨域等桶级配置用 `bucket.Client()` 拿到原生客户端操作。
