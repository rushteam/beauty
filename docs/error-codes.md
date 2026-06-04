# 结构化错误码

`pkg/errors` 提供三层统一的错误体系：

```
业务 Code（如 10404）
    ↓ 自动映射
gRPC status code（codes.NotFound）
    ↓ 自动映射
HTTP status code（404）+ JSON body
```

handler 只需返回 `*errors.Status`，recovery 中间件和拦截器自动完成转换，无需手写 `w.WriteHeader` 或 `status.Error`。

---

## 快速上手

```go
import apperrors "github.com/rushteam/beauty/pkg/errors"

// gRPC handler
func (s *UserSvc) GetUser(ctx context.Context, req *pb.GetUserReq) (*pb.User, error) {
    user, err := s.repo.Find(ctx, req.Id)
    if err != nil {
        return nil, apperrors.NotFound("user not found")
    }
    return user, nil
}

// HTTP handler（需配合 HTTPMiddlewareErrorHandler 中间件）
func GetOrder(w http.ResponseWriter, r *http.Request) {
    order, err := svc.Find(r.Context(), id)
    if err != nil {
        apperrors.SetError(r.Context(), apperrors.NotFound("order not found"))
        return
    }
    json.NewEncoder(w).Encode(order)
}
```

客户端收到的 HTTP 响应：

```json
HTTP/1.1 404 Not Found
Content-Type: application/json

{"code": 404, "message": "order not found"}
```

---

## 接入框架

### gRPC 服务端

`grpcserver.New` 已默认挂载 recovery 拦截器，它会识别 `*errors.Status` 并自动转换。无需额外配置。

如需显式挂载 gRPC 错误转换拦截器（不经过 recovery）：

```go
import (
    apperrors "github.com/rushteam/beauty/pkg/errors"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
)

beauty.WithGrpcServer(":9090", register,
    grpcserver.WithGrpcServerUnaryInterceptor(
        apperrors.GRPCUnaryServerInterceptor,
    ),
    grpcserver.WithGrpcServerStreamInterceptor(
        apperrors.GRPCStreamServerInterceptor,
    ),
)
```

### HTTP 服务端

**方式一（推荐）：** 在 recovery 中间件基础上加 `HTTPMiddlewareErrorHandler`，handler 内用 `SetError` 写入错误：

```go
import (
    apperrors "github.com/rushteam/beauty/pkg/errors"
    "github.com/rushteam/beauty/pkg/middleware/recovery"
)

beauty.WithWebServer(":8080", mux,
    webserver.WithMiddleware(recovery.HTTPMiddleware()),                    // 兜底 panic
    webserver.WithMiddleware(apperrors.HTTPMiddlewareErrorHandler),         // 错误转 JSON
)

// handler 内
func MyHandler(w http.ResponseWriter, r *http.Request) {
    if err := validate(r); err != nil {
        apperrors.SetError(r.Context(), apperrors.InvalidArgument(err.Error()))
        return
    }
    // 正常逻辑...
}
```

**方式二：** handler 直接调 `WriteHTTP`，适合明确知道错误类型的场景：

```go
func MyHandler(w http.ResponseWriter, r *http.Request) {
    user, err := svc.GetUser(r.Context(), id)
    if err != nil {
        if s, ok := apperrors.FromError(err); ok {
            apperrors.WriteHTTP(w, s)
        } else {
            apperrors.WriteHTTP(w, apperrors.Internal("unexpected error"))
        }
        return
    }
    json.NewEncoder(w).Encode(user)
}
```

---

## 内置错误码

| 函数 | Code | HTTP | gRPC |
|------|------|------|------|
| `InvalidArgument(msg)` | 400 | 400 | INVALID_ARGUMENT |
| `Unauthenticated(msg)` | 401 | 401 | UNAUTHENTICATED |
| `Forbidden(msg)` | 403 | 403 | PERMISSION_DENIED |
| `NotFound(msg)` | 404 | 404 | NOT_FOUND |
| `Conflict(msg)` | 409 | 409 | ALREADY_EXISTS |
| `TooManyRequests(msg)` | 429 | 429 | RESOURCE_EXHAUSTED |
| `Internal(msg)` | 500 | 500 | INTERNAL |
| `Unimplemented(msg)` | 501 | 501 | UNIMPLEMENTED |
| `Unavailable(msg)` | 503 | 503 | UNAVAILABLE |

---

## 自定义业务错误码

业务模块在自己的 `init()` 中注册，框架保留 1–999，业务从 1000 起：

```go
// internal/errors/codes.go
package errors

import apperrors "github.com/rushteam/beauty/pkg/errors"

const (
    ErrUserNotFound  = apperrors.Code(10404)
    ErrUserExist     = apperrors.Code(10409)
    ErrOrderExpired  = apperrors.Code(20422)
)

func init() {
    apperrors.Register(ErrUserNotFound, 404, 5 /*NOT_FOUND*/,     "user not found")
    apperrors.Register(ErrUserExist,    409, 6 /*ALREADY_EXISTS*/, "user already exists")
    apperrors.Register(ErrOrderExpired, 422, 9 /*FAILED_PRECONDITION*/, "order expired")
}
```

使用：

```go
return apperrors.New(ErrUserNotFound, "").
    WithDetail(&apperrors.ResourceInfo{ResourceType: "User", Name: userID})
```

或直接用 `Code` 值：

```go
return apperrors.Newf(errors.ErrUserNotFound, "user %s not found", userID)
```

---

## 结构化详情（Details）

Details 让调用方机器可读地知道出了什么问题：

```go
// 参数校验失败，告知哪些字段有问题
return apperrors.InvalidArgument("request validation failed").
    WithDetail(&apperrors.FieldViolation{Field: "email", Description: "must be a valid email"}).
    WithDetail(&apperrors.FieldViolation{Field: "age",   Description: "must be >= 0"})

// 限流，告知客户端何时可以重试
return apperrors.TooManyRequests("rate limit exceeded").
    WithDetail(&apperrors.RetryInfo{RetryDelay: 5 * time.Second})

// 资源不存在，告知是哪个资源
return apperrors.NotFound("resource not found").
    WithDetail(&apperrors.ResourceInfo{ResourceType: "Order", Name: orderID})

// 携带机器可读的错误原因和扩展信息
return apperrors.Forbidden("operation not allowed").
    WithDetail(&apperrors.ErrorInfo{
        Reason:   "INSUFFICIENT_PERMISSION",
        Domain:   "order-service",
        Metadata: map[string]string{"required_role": "admin"},
    })
```

客户端收到的 JSON：

```json
{
  "code": 400,
  "message": "request validation failed",
  "details": [
    {"type": "FieldViolation", "data": {"Field": "email", "Description": "must be a valid email"}},
    {"type": "FieldViolation", "data": {"Field": "age",   "Description": "must be >= 0"}}
  ]
}
```

---

## 原始错误（Cause）

`WithCause` 记录底层错误，**只用于服务端日志，永远不序列化给客户端**：

```go
user, err := db.QueryUser(ctx, id)
if err != nil {
    return apperrors.Internal("failed to get user").WithCause(err)
    // err 的内容（如 SQL 语句、连接信息）不会出现在响应里
}
```

---

## 错误判断

```go
// 按 Code 判断
if s, ok := apperrors.FromError(err); ok {
    switch s.Code() {
    case apperrors.CodeNotFound:
        // 处理 404
    case apperrors.CodeUnauthenticated:
        // 处理 401
    }
}

// errors.Is 风格（按 Code 匹配，忽略 message）
notFoundSentinel := apperrors.New(apperrors.CodeNotFound, "")
if errors.Is(err, notFoundSentinel) {
    // err 是 NotFound 类型
}

// 从 gRPC 错误还原
if s, ok := apperrors.FromGRPCError(grpcErr); ok {
    // s.Code() 是框架 Code
}
```

---

## 渐进式迁移

旧代码返回 `fmt.Errorf(...)` 不需要立即改，recovery 中间件识别不到 `*Status` 时兜底返回 500，行为与之前完全一致。按模块逐步替换即可：

```go
// 迁移前
return nil, fmt.Errorf("user not found: %w", err)  // → 500

// 迁移后
return nil, apperrors.NotFound("user not found").WithCause(err)  // → 404
```
