package compress

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

var gzipPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(nil)
	},
}

var compressibleTypes = []string{
	"text/",
	"application/json",
	"application/xml",
	"application/javascript",
}

func isCompressible(contentType string) bool {
	ct := strings.ToLower(contentType)
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	for _, t := range compressibleTypes {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	minSize     int
	buf         []byte
	wroteHeader bool
	statusCode  int
	compressed  bool
	done        bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.statusCode = code
	g.wroteHeader = true
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.statusCode = http.StatusOK
		g.wroteHeader = true
	}

	if g.done {
		return g.ResponseWriter.Write(b)
	}

	if g.compressed {
		return g.gz.Write(b)
	}

	g.buf = append(g.buf, b...)

	ct := g.ResponseWriter.Header().Get("Content-Type")
	if !isCompressible(ct) {
		g.flush(false)
		return len(b), nil
	}

	if g.minSize > 0 && len(g.buf) < g.minSize {
		return len(b), nil
	}

	g.flush(true)
	return len(b), nil
}

func (g *gzipResponseWriter) flush(compress bool) {
	g.done = true
	if compress {
		g.compressed = true
		g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
		g.ResponseWriter.Header().Del("Content-Length")
		g.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
		g.ResponseWriter.WriteHeader(g.statusCode)
		g.gz.Write(g.buf)
	} else {
		g.ResponseWriter.WriteHeader(g.statusCode)
		g.ResponseWriter.Write(g.buf)
	}
	g.buf = nil
}

func (g *gzipResponseWriter) close() {
	if !g.done && len(g.buf) > 0 {
		ct := g.ResponseWriter.Header().Get("Content-Type")
		if isCompressible(ct) && (g.minSize == 0 || len(g.buf) >= g.minSize) {
			g.flush(true)
		} else {
			g.flush(false)
		}
	} else if !g.done {
		if !g.wroteHeader {
			g.statusCode = http.StatusOK
		}
		g.ResponseWriter.WriteHeader(g.statusCode)
	}
	if g.compressed {
		g.gz.Close()
	}
}

// Flush 支持流式响应（SSE / chunked）。没有它，handler 调用 Flush 不会真正下发数据，
// 全缓冲到 minSize 或 handler 返回才发——流式场景会被破坏。
// 首次 Flush 时必须定下"压缩与否"，因为流式响应无法再等 minSize 累积。
func (g *gzipResponseWriter) Flush() {
	if !g.done {
		ct := g.ResponseWriter.Header().Get("Content-Type")
		g.flush(isCompressible(ct))
	}
	if g.compressed {
		// 把 gzip 内部缓冲推到底层，否则压缩数据可能滞留
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 暴露底层 ResponseWriter，便于 http.ResponseController 及 Hijacker
// 等可选接口透传（如 WebSocket 升级）。
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

func Middleware(minSize int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			gz := gzipPool.Get().(*gzip.Writer)
			gz.Reset(w)
			defer func() {
				gzipPool.Put(gz)
			}()

			grw := &gzipResponseWriter{
				ResponseWriter: w,
				gz:             gz,
				minSize:        minSize,
			}
			defer grw.close()

			next.ServeHTTP(grw, r)
		})
	}
}
