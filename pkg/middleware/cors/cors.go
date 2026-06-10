package cors

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

type Config struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAge           int
}

func Default() *Config {
	return &Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"},
		AllowHeaders:     []string{"Content-Type", "Authorization", "X-Request-ID"},
		AllowCredentials: false,
		MaxAge:           86400,
	}
}

func (c *Config) allowMethods() string {
	if len(c.AllowMethods) == 0 {
		return "GET,POST,PUT,PATCH,DELETE,OPTIONS,HEAD"
	}
	return strings.Join(c.AllowMethods, ",")
}

func (c *Config) allowHeaders() string {
	if len(c.AllowHeaders) == 0 {
		return "Content-Type,Authorization,X-Request-ID"
	}
	return strings.Join(c.AllowHeaders, ",")
}

func (c *Config) maxAge() string {
	if c.MaxAge == 0 {
		return "86400"
	}
	return strconv.Itoa(c.MaxAge)
}

func (c *Config) isOriginAllowed(origin string) bool {
	for _, o := range c.AllowOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}

func (c *Config) hasWildcard() bool {
	return slices.Contains(c.AllowOrigins, "*")
}

func (c *Config) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" {
				// 决定回显的 Allow-Origin。注意：带 credentials 时浏览器禁止 "*"，
				// 必须回显具体 origin，否则响应被浏览器丢弃且等于把凭证暴露给任意源。
				allowOrigin := ""
				switch {
				case c.hasWildcard() && c.AllowCredentials:
					allowOrigin = origin // 通配符 + 凭证：回显具体 origin
				case c.hasWildcard():
					allowOrigin = "*"
				case c.isOriginAllowed(origin):
					allowOrigin = origin
				}

				if allowOrigin != "" {
					w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
					if allowOrigin != "*" {
						w.Header().Add("Vary", "Origin")
					}
					// 仅在回显具体 origin（非 "*"）时才发 credentials 头
					if c.AllowCredentials && allowOrigin != "*" {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
					if len(c.ExposeHeaders) > 0 {
						w.Header().Set("Access-Control-Expose-Headers", strings.Join(c.ExposeHeaders, ","))
					}
				}
			}

			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", c.allowMethods())
				w.Header().Set("Access-Control-Allow-Headers", c.allowHeaders())
				w.Header().Set("Access-Control-Max-Age", c.maxAge())
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
