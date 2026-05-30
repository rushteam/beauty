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
