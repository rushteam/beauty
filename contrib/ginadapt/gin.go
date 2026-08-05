// Package ginadapt 将标准 net/http 中间件适配为 Gin 中间件。
//
// beauty 框架的 HTTP 中间件统一使用标准签名 func(http.Handler) http.Handler，
// 本包提供 Wrap 函数将其转为 gin.HandlerFunc，使 antireplay、signverify、auth
// 等中间件可直接在 Gin 路由中使用。
//
// 用法：
//
//	import adaptgin "github.com/rushteam/beauty/contrib/ginadapt"
//
//	r := gin.Default()
//	r.Use(adaptgin.Wrap(antireplay.HTTPMiddleware(store)))
//	r.Use(adaptgin.Wrap(signverify.HTTPMiddleware(getSecret,
//	    signverify.WithExtractUser(),
//	)))
//
//	r.POST("/pay", func(c *gin.Context) {
//	    user, _ := auth.GetUserFromContext(c.Request.Context())
//	    c.JSON(200, gin.H{"user_id": user.ID()})
//	})
package ginadapt

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Wrap 将标准 net/http 中间件转为 gin.HandlerFunc。
//
// 适配逻辑：
//   - 中间件对 request 的修改（如 r.WithContext 注入 auth.User）会回写到 gin.Context
//   - 中间件拒绝请求时（直接写响应不调用 next），自动调用 c.Abort() 阻止后续 handler
func Wrap(mw func(http.Handler) http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		var called bool
		wrapped := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Request = r
			called = true
			c.Next()
		}))
		wrapped.ServeHTTP(c.Writer, c.Request)
		if !called {
			c.Abort()
		}
	}
}
