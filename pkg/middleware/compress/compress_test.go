package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 流式响应：handler 调用 Flush 后数据应立即下发到底层 writer，
// 而不是缓冲到 minSize 或 handler 返回。否则 SSE/chunked 会被破坏。
func TestCompress_FlushStreams(t *testing.T) {
	mw := Middleware(1 << 20) // minSize 设很大，确保不是因 minSize 触发下发

	rec := httptest.NewRecorder()
	var bytesAtFlush int
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "hello streaming")

		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("gzipResponseWriter must implement http.Flusher")
			return
		}
		f.Flush()
		bytesAtFlush = rec.Body.Len() // Flush 时底层应已收到数据
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if bytesAtFlush == 0 {
		t.Fatal("no data reached the underlying writer at Flush time — streaming is broken")
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("want gzip encoding, got %q", enc)
	}

	// 解压应还原完整内容
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if string(got) != "hello streaming" {
		t.Fatalf("want %q, got %q", "hello streaming", string(got))
	}
}

// Unwrap 应暴露底层 ResponseWriter。
func TestCompress_Unwrap(t *testing.T) {
	base := httptest.NewRecorder()
	grw := &gzipResponseWriter{ResponseWriter: base}
	if grw.Unwrap() != base {
		t.Fatal("Unwrap must return the underlying ResponseWriter")
	}
}
