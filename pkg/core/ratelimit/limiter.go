package ratelimit

import (
	"golang.org/x/time/rate"
)

type Limiter struct {
	limiter *rate.Limiter
}

func NewLimiter(r float64, b int) *Limiter {
	return &Limiter{
		limiter: rate.NewLimiter(rate.Limit(r), b),
	}
}

func (l *Limiter) Allow() bool {
	return l.limiter.Allow()
}
