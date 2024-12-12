package middleware

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/availability/circuit"
	"github.com/rushteam/beauty/pkg/availability/ratelimit"
)

func CircuitBreaker(b *circuit.Breaker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := b.Execute(func() error {
				next.ServeHTTP(w, r)
				return nil
			})
			if err != nil {
				http.Error(w, err.Error(), 500)
			}
		})
	}
}

func RateLimit(l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow() {
				http.Error(w, "rate limit exceeded", 429)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
