package resty_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	httpclient "github.com/rushteam/beauty/pkg/client/http"
	"github.com/rushteam/beauty/pkg/service/discover"
)

// 先 5xx、后网络错误:不能把已被关闭 body 的 5xx 响应当作结果返回。
func TestRoundTrip_NoClosedBodyAfter5xxThenNetworkError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer srv.Close()

	disc := &mockDiscovery{services: []discover.ServiceInfo{newService(srv.Listener.Addr().String(), 1, nil)}}
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc", httpclient.WithHTTPMaxRetries(1))
	req, err := cli.NewRequest(t.Context(), http.MethodGet, "/")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cli.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if _, rerr := io.ReadAll(resp.Body); rerr != nil {
			t.Fatalf("returned resp %d with unreadable body: %v", resp.StatusCode, rerr)
		}
		t.Fatalf("want network error from last attempt, got resp %d", resp.StatusCode)
	}
}

type seenRequest struct {
	contentLength    int64
	transferEncoding []string
	body             string
}

// 无 GetBody 的流式 body 在重试/不重试两种模式下都应带真实 Content-Length 发出,而不是 chunked。
func TestRoundTrip_PreservesContentLength(t *testing.T) {
	for _, tc := range []struct {
		name    string
		retries int
	}{
		{"no-retry-passthrough", 0},
		{"retry-buffered", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan seenRequest, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				seen <- seenRequest{r.ContentLength, r.TransferEncoding, string(b)}
			}))
			defer srv.Close()

			disc := &mockDiscovery{services: []discover.ServiceInfo{newService(srv.Listener.Addr().String(), 1, nil)}}
			cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "test-svc", httpclient.WithHTTPMaxRetries(tc.retries))
			req, err := cli.NewRequest(t.Context(), http.MethodPost, "/upload")
			if err != nil {
				t.Fatal(err)
			}
			payload := strings.Repeat("x", 4096)
			// io.NopCloser 包一层使 http 无法识别具体类型,模拟没有 GetBody 的流式 body
			req.Body = io.NopCloser(strings.NewReader(payload))
			req.ContentLength = int64(len(payload))

			resp, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			got := <-seen
			if got.contentLength != int64(len(payload)) || len(got.transferEncoding) != 0 {
				t.Fatalf("content-length=%d transfer-encoding=%v, want %d and none",
					got.contentLength, got.transferEncoding, len(payload))
			}
			if got.body != payload {
				t.Fatalf("body mismatch: got %d bytes", len(got.body))
			}
		})
	}
}
