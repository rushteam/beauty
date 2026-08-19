# contrib/ginadapt —— Gin ↔ beauty HTTP 中间件适配(独立模块)

beauty 的 HTTP 中间件统一签名 `func(http.Handler) http.Handler`(accesslog、auth、signverify、
antireplay、circuitbreaker…)。本包提供 `Wrap`,将其转为 `gin.HandlerFunc`,在 Gin 项目中复用
同一套中间件,无需重写。

```bash
go get github.com/rushteam/beauty/contrib/ginadapt@latest
```

## 用法

```go
import (
    adaptgin "github.com/rushteam/beauty/contrib/ginadapt"
    "github.com/rushteam/beauty/pkg/middleware/accesslog"
    "github.com/rushteam/beauty/pkg/middleware/auth"
)

r := gin.Default()
r.Use(adaptgin.Wrap(accesslog.HTTP()))
r.Use(adaptgin.Wrap(auth.HTTPMiddleware(verifier)))

r.GET("/me", func(c *gin.Context) {
    user, _ := auth.GetUserFromContext(c.Request.Context())
    c.JSON(200, gin.H{"id": user.ID()})
})
```

## 适配语义

- 中间件对 `*http.Request` 的修改(如 `r.WithContext` 注入 auth.User)会回写到 `gin.Context`
- 中间件直接写响应、不调用 `next` 时,自动 `c.Abort()` 阻止后续 handler

## 边界

仅做签名适配,不包含 Gin 路由或 binding 逻辑。不依赖 beauty 核心,只依赖 `gin-gonic/gin`。
Gin 原生中间件无法反向适配到 `net/http`,方向是单向的。
