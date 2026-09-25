// Package sensitive 提供即插即用的 HTTP 敏感词中间件:基于 pkg/foundation/actrie 的
// Aho-Corasick 自动机,一次扫描请求体(可选含 query)里的所有敏感词,支持两种处置:
//
//   - ModeBlock(拦截):命中即返回 403,不进入业务处理器;
//   - ModeMask(脱敏):把命中词替换为遮罩字符后,改写请求体再放行,业务侧读到的已是干净文本。
//
// 只扫描文本类请求(application/json、text/*、application/x-www-form-urlencoded);其余
// Content-Type 透传。请求体超过 MaxScanBytes 时按"过大不处理"透传(检测仍扫描前缀,脱敏跳过),
// 避免破坏大文件上传。
//
// 归一化(反变体绕过 c0l0r / 敏-感-词)由构造 Matcher 时的 actrie.WithNormalizer 决定,
// 本中间件不重复该逻辑。
//
// 快速开始:
//
//	mux.Use(sensitive.Block("敏感词", "违禁"))          // 命中即 403
//	mux.Use(sensitive.Mask("敏感词", "违禁"))           // 命中自动打码后放行
//
// 或完全自定义(复用已编译词典、接管响应、记日志):
//
//	m := actrie.New(actrie.WithNormalizer(actrie.FoldCase()))
//	m.AddAll(words...)
//	mux.Use(sensitive.Middleware(sensitive.Config{Matcher: m, Mode: sensitive.ModeBlock}))
package sensitive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rushteam/beauty/pkg/foundation/actrie"
)

// Mode 命中后的处置方式。
type Mode int

const (
	// ModeBlock 命中敏感词即拦截,返回 403,不进入业务处理器。
	ModeBlock Mode = iota
	// ModeMask 命中敏感词则遮罩后放行,业务侧读到脱敏文本。
	ModeMask
)

// 默认扫描的文本类 Content-Type 前缀。
var defaultContentTypes = []string{
	"application/json",
	"application/x-www-form-urlencoded",
	"text/",
}

const defaultMaxScanBytes int64 = 4 << 20 // 4 MB

// Config 敏感词中间件配置。
type Config struct {
	// Matcher 已编译的敏感词自动机(必填)。可携带 actrie.WithNormalizer 做反绕过。
	Matcher *actrie.Matcher
	// Mode 命中处置:ModeBlock(默认)或 ModeMask。
	Mode Mode
	// MaskRune 脱敏模式下的遮罩字符,默认 '*'。
	MaskRune rune
	// MaxScanBytes 单请求最大扫描字节数,默认 4MB;超过则透传(检测扫前缀,脱敏跳过)。
	MaxScanBytes int64
	// ScanQuery 是否也扫描 URL query(仅用于拦截检测,脱敏模式不改写 query)。
	ScanQuery bool
	// ContentTypes 需要扫描的 Content-Type 前缀;为空用默认文本类集合。
	ContentTypes []string
	// SkipPaths 跳过检测的路径前缀(如 /health、/metrics)。
	SkipPaths []string
	// OnBlocked 拦截时的响应处理;nil 时返回 403 + 通用 JSON(不泄露命中词)。
	// hit 为首个命中的敏感词,供记录审计(不建议回给客户端)。
	OnBlocked func(w http.ResponseWriter, r *http.Request, hit string)
}

// Block 便捷构造:命中任一词即返回 403(大小写敏感、无归一化)。
func Block(words ...string) func(http.Handler) http.Handler {
	m := actrie.New()
	m.AddAll(words...)
	return Middleware(Config{Matcher: m, Mode: ModeBlock})
}

// Mask 便捷构造:命中词自动替换为 '*' 后放行。
func Mask(words ...string) func(http.Handler) http.Handler {
	m := actrie.New()
	m.AddAll(words...)
	return Middleware(Config{Matcher: m, Mode: ModeMask})
}

// Middleware 按 cfg 返回敏感词中间件。Matcher 为 nil 时透传(不做任何检测)。
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.MaskRune == 0 {
		cfg.MaskRune = '*'
	}
	if cfg.MaxScanBytes <= 0 {
		cfg.MaxScanBytes = defaultMaxScanBytes
	}
	if len(cfg.ContentTypes) == 0 {
		cfg.ContentTypes = defaultContentTypes
	}
	if cfg.OnBlocked == nil {
		cfg.OnBlocked = defaultOnBlocked
	}

	return func(next http.Handler) http.Handler {
		if cfg.Matcher == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hasPrefixAny(r.URL.Path, cfg.SkipPaths) {
				next.ServeHTTP(w, r)
				return
			}

			// 1) 先扫 query(仅拦截检测)
			if cfg.ScanQuery && r.URL.RawQuery != "" {
				if q, err := url.QueryUnescape(r.URL.RawQuery); err == nil {
					if hit, ok := cfg.Matcher.FindFirst(q); ok {
						if cfg.Mode == ModeBlock {
							cfg.OnBlocked(w, r, hit.Pattern)
							return
						}
					}
				}
			}

			// 2) 扫描/改写请求体
			if !cfg.shouldScanBody(r) {
				next.ServeHTTP(w, r)
				return
			}
			buf, truncated, err := readLimited(r.Body, cfg.MaxScanBytes)
			if err != nil {
				next.ServeHTTP(w, r) // 读取失败不阻断业务
				return
			}
			scanText := string(buf)

			switch cfg.Mode {
			case ModeBlock:
				if hit, ok := cfg.Matcher.FindFirst(scanText); ok {
					_ = r.Body.Close()
					cfg.OnBlocked(w, r, hit.Pattern)
					return
				}
				restoreBody(r, buf, truncated)
			case ModeMask:
				if truncated {
					restoreBody(r, buf, truncated) // 过大不脱敏,原样透传
				} else {
					masked := cfg.Matcher.Replace(scanText, cfg.MaskRune)
					_ = r.Body.Close()
					nb := []byte(masked)
					r.Body = io.NopCloser(bytes.NewReader(nb))
					r.ContentLength = int64(len(nb))
					r.Header.Set("Content-Length", strconv.Itoa(len(nb)))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// shouldScanBody 判断该请求体是否需要扫描。
func (cfg Config) shouldScanBody(r *http.Request) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return false
	}
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i] // 去掉 charset 等参数
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	return hasPrefixAny(ct, cfg.ContentTypes)
}

// readLimited 读取最多 max 字节;返回是否被截断(body 超过 max)。
func readLimited(body io.Reader, max int64) (buf []byte, truncated bool, err error) {
	buf, err = io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > max {
		return buf, true, nil
	}
	return buf, false, nil
}

// restoreBody 把读出的内容放回 r.Body,供后续处理器完整读取。
func restoreBody(r *http.Request, buf []byte, truncated bool) {
	if truncated {
		// 已读的前缀 + 未读的剩余,拼回;保留原 body 的 Close。
		orig := r.Body
		r.Body = &multiReadCloser{
			r: io.MultiReader(bytes.NewReader(buf), orig),
			c: orig,
		}
		return
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(buf))
}

// multiReadCloser 组合一个 Reader 与底层 Closer(用于截断透传时不泄露原 body)。
type multiReadCloser struct {
	r io.Reader
	c io.Closer
}

func (m *multiReadCloser) Read(p []byte) (int, error) { return m.r.Read(p) }
func (m *multiReadCloser) Close() error               { return m.c.Close() }

func defaultOnBlocked(w http.ResponseWriter, _ *http.Request, _ string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    http.StatusForbidden,
		"message": "内容包含违禁词",
	})
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
