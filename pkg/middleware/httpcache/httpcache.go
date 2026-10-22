// Package httpcache 提供 HTTP Cache-Control 策略中间件：按路径规则写入缓存头，
// 方便 Service Worker 按 Network First / Cache First / stale-while-revalidate 决策。
//
// 机制、不政策：只设置响应头；是否缓存由浏览器 / SW 决定。
// 若下游已自行设置 Cache-Control，则尊重下游、不覆盖。
package httpcache

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Policy 描述一条 Cache-Control 策略。
type Policy struct {
	// MaxAge 对应 max-age=N（秒）。0 且未设 NoStore/NoCache 时不写 max-age。
	MaxAge time.Duration
	// SWR 对应 stale-while-revalidate=N（秒）。0 表示不写。
	SWR time.Duration
	// StaleIfError 对应 stale-if-error=N（秒）。0 表示不写。
	StaleIfError time.Duration
	// Private 为 true 写 private，否则在存在可缓存指令时写 public。
	Private bool
	// NoStore 写 no-store（优先于其它缓存指令）。
	NoStore bool
	// NoCache 写 no-cache。
	NoCache bool
	// MustRevalidate 写 must-revalidate。
	MustRevalidate bool
	// Immutable 写 immutable（常与长 max-age 搭配用于带 hash 的静态资源）。
	Immutable bool
}

// Header 生成 Cache-Control 头值；无可写指令时返回空串。
func (p Policy) Header() string {
	if p.NoStore {
		return "no-store"
	}
	var parts []string
	if p.NoCache {
		parts = append(parts, "no-cache")
	}
	if p.Private {
		parts = append(parts, "private")
	} else if p.MaxAge > 0 || p.SWR > 0 || p.StaleIfError > 0 || p.Immutable {
		parts = append(parts, "public")
	}
	if p.MaxAge > 0 {
		parts = append(parts, fmt.Sprintf("max-age=%d", int(p.MaxAge.Seconds())))
	}
	if p.SWR > 0 {
		parts = append(parts, fmt.Sprintf("stale-while-revalidate=%d", int(p.SWR.Seconds())))
	}
	if p.StaleIfError > 0 {
		parts = append(parts, fmt.Sprintf("stale-if-error=%d", int(p.StaleIfError.Seconds())))
	}
	if p.MustRevalidate {
		parts = append(parts, "must-revalidate")
	}
	if p.Immutable {
		parts = append(parts, "immutable")
	}
	return strings.Join(parts, ", ")
}

// Rule 按路径前缀匹配策略（最长前缀优先）。
type Rule struct {
	Prefix string
	Policy Policy
}

// 常用预设。
var (
	// NoStore API 默认：不缓存。
	NoStore = Policy{NoStore: true}
	// AppShell HTML 壳：短缓存 + SWR。
	AppShell = Policy{MaxAge: 60 * time.Second, SWR: 5 * time.Minute, Private: true}
	// HashedAsset 带 hash 的静态资源：长缓存 + immutable。
	HashedAsset = Policy{MaxAge: 365 * 24 * time.Hour, Immutable: true}
	// PublicShort 可公开短缓存的只读 API。
	PublicShort = Policy{MaxAge: 30 * time.Second, SWR: 60 * time.Second}
)

// Middleware 返回按规则设置 Cache-Control 的中间件。
// rules 按 Prefix 长度降序匹配；未命中时用 fallback（零值表示不设）。
// 在首次 WriteHeader/Write 前写入头；下游已设 Cache-Control 则不覆盖。
func Middleware(fallback Policy, rules ...Rule) func(http.Handler) http.Handler {
	ordered := append([]Rule(nil), rules...)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && len(ordered[j-1].Prefix) < len(ordered[j].Prefix); j-- {
			ordered[j-1], ordered[j] = ordered[j], ordered[j-1]
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := fallback
			for _, rule := range ordered {
				if rule.Prefix == "" || strings.HasPrefix(r.URL.Path, rule.Prefix) {
					p = rule.Policy
					break
				}
			}
			hw := &headerWriter{ResponseWriter: w, policy: p}
			next.ServeHTTP(hw, r)
		})
	}
}

// headerWriter 在首次写出响应前注入 Cache-Control。
type headerWriter struct {
	http.ResponseWriter
	policy Policy
	wrote  bool
}

func (h *headerWriter) apply() {
	if h.Header().Get("Cache-Control") != "" {
		return
	}
	if v := h.policy.Header(); v != "" {
		h.Header().Set("Cache-Control", v)
	}
}

func (h *headerWriter) WriteHeader(code int) {
	if !h.wrote {
		h.apply()
		h.wrote = true
	}
	h.ResponseWriter.WriteHeader(code)
}

func (h *headerWriter) Write(b []byte) (int, error) {
	if !h.wrote {
		h.apply()
		h.wrote = true
	}
	return h.ResponseWriter.Write(b)
}
