package elasticsearch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	es "github.com/rushteam/beauty/contrib/elasticsearch"
)

// mockES 起一个打桩 ES 服务。v8 客户端首个请求会做 product check,故响应须带
// X-Elastic-Product: Elasticsearch 头。
func mockES(t *testing.T, handler http.HandlerFunc) (*es.Client, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		handler(w, r)
	}))
	client, err := es.New(es.Config{Addresses: []string{srv.URL}})
	if err != nil {
		srv.Close()
		t.Fatalf("new: %v", err)
	}
	return client, srv.Close
}

func TestPing(t *testing.T) {
	client, stop := mockES(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":{"number":"8.16.0"},"tagline":"You Know, for Search"}`))
	})
	defer stop()

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestSearch(t *testing.T) {
	var gotPath, gotBody string
	client, stop := mockES(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_search") {
			gotPath = r.URL.Path
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			gotBody = string(b)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"hits":{"total":{"value":1},"hits":[{"_id":"1","_source":{"name":"alice"}}]}}`))
			return
		}
		// info / product check
		_, _ = w.Write([]byte(`{"version":{"number":"8.16.0"}}`))
	})
	defer stop()

	query := []byte(`{"query":{"match":{"name":"alice"}}}`)
	raw, err := client.Search(context.Background(), "users", query)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// 返回的是原始响应 JSON,能解析出 hits。
	var resp struct {
		Hits struct {
			Total struct{ Value int } `json:"total"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Hits.Total.Value != 1 {
		t.Fatalf("hits.total = %d, want 1", resp.Hits.Total.Value)
	}
	if !strings.Contains(gotPath, "/users/_search") {
		t.Fatalf("请求路径 = %q, 应含 /users/_search", gotPath)
	}
	if !strings.Contains(gotBody, "alice") {
		t.Fatalf("请求体未透传查询: %q", gotBody)
	}
}

func TestSearch_ErrorStatus(t *testing.T) {
	client, stop := mockES(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_search") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad query"}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":{"number":"8.16.0"}}`))
	})
	defer stop()

	if _, err := client.Search(context.Background(), "users", []byte(`{}`)); err == nil {
		t.Fatal("4xx 应返回错误")
	}
}
