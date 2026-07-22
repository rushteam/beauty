// Package etag 提供 HTTP ETag / 条件请求中间件:为 GET 响应计算强 ETag,并在客户端带
// If-None-Match 命中时回 304 Not Modified,省去重复传输的带宽。
//
// 实现:缓冲下游响应体,对 200 的 GET 响应用其内容算 ETag(FNV-1a 十六进制,强校验器)。
// 因需缓冲整个响应体,适合 JSON API 等中小响应的按需接入;流式/大响应不建议套用。
// 若下游已自行设置 ETag,则尊重下游、不覆盖。
package etag

import (
	"bytes"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
)

// Middleware 返回 ETag 中间件。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只对 GET 处理条件请求(HEAD 无体、其它方法多为写操作)。
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// 仅对 200、且下游未自设 ETag 的响应计算。
		if rec.status != http.StatusOK || w.Header().Get("ETag") != "" {
			rec.flush()
			return
		}
		tag := computeETag(rec.buf.Bytes())
		w.Header().Set("ETag", tag)
		if match := r.Header.Get("If-None-Match"); match != "" && etagMatch(match, tag) {
			// 命中:回 304,不带响应体(保留 ETag 头,清掉 Content-Length)。
			w.Header().Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		rec.flush()
	})
}

// computeETag 用响应体算强 ETag:"<hex>"。
func computeETag(body []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(body)
	return `"` + strconv.FormatUint(h.Sum64(), 16) + `"`
}

// etagMatch 判断 If-None-Match 头是否命中 tag。支持 "*"、逗号分隔的多值,以及弱校验器前缀 W/。
func etagMatch(header, tag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == tag {
			return true
		}
	}
	return false
}

// recorder 缓冲下游响应(状态码 + 响应体),以便算 ETag 后决定回体还是回 304。
// 响应头直接写在底层 ResponseWriter 的 Header() 上(下游正常设置)。
type recorder struct {
	http.ResponseWriter
	status  int
	buf     bytes.Buffer
	written bool
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.written = true
}

func (r *recorder) Write(b []byte) (int, error) {
	r.written = true
	return r.buf.Write(b)
}

// flush 把缓冲的状态码与响应体真正写入底层 ResponseWriter。
func (r *recorder) flush() {
	r.ResponseWriter.WriteHeader(r.status)
	_, _ = r.ResponseWriter.Write(r.buf.Bytes())
}
