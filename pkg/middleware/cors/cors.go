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
				if c.hasWildcard() {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if c.isOriginAllowed(origin) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}

				if c.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}

				if len(c.ExposeHeaders) > 0 {
					w.Header().Set("Access-Control-Expose-Headers", strings.Join(c.ExposeHeaders, ","))
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
