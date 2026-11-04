package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func TestOAIUsageToUsage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want llm.Usage
	}{
		{
			name: "cached_tokens",
			raw:  `{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":8}}`,
			want: llm.Usage{InputTokens: 10, OutputTokens: 2, CacheReadInputTokens: 8},
		},
		{
			name: "cache_read wins",
			raw:  `{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":8,"cache_read_tokens":9,"cache_creation_tokens":3}}`,
			want: llm.Usage{InputTokens: 10, OutputTokens: 2, CacheReadInputTokens: 9, CacheCreationInputTokens: 3},
		},
		{
			name: "ignore junk",
			raw:  `{"prompt_tokens":1,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":"n/a","cache_creation_tokens":-5}}`,
			want: llm.Usage{InputTokens: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var u oaiUsage
			if err := json.Unmarshal([]byte(tt.raw), &u); err != nil {
				t.Fatal(err)
			}
			got := u.toUsage()
			if got != tt.want {
				t.Fatalf("usage = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGenerate_CacheTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"model":"m",
			"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":6,"cache_creation_tokens":2}}
		}`)
	}))
	defer srv.Close()

	resp, err := New("k", WithBaseURL(srv.URL)).Generate(context.Background(), llm.Request{
		Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "q"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CacheReadInputTokens != 6 || resp.Usage.CacheCreationInputTokens != 2 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestStream_CacheTokens(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":4}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var got llm.Usage
	var text string
	for c, err := range New("k", WithBaseURL(srv.URL)).Stream(context.Background(), llm.Request{
		Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "q"}},
	}) {
		if err != nil {
			t.Fatal(err)
		}
		text += c.Delta
		if c.Usage != nil {
			got = *c.Usage
		}
	}
	if text != "hi" {
		t.Fatalf("text = %q", text)
	}
	if got.CacheReadInputTokens != 4 || got.InputTokens != 10 {
		t.Fatalf("usage = %+v", got)
	}
	if !json.Valid([]byte(body)) || !strings.Contains(body, `"include_usage":true`) {
		t.Fatalf("stream request = %s", body)
	}
}

func TestGenerate_FileID(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	_, err := New("k", WithBaseURL(srv.URL)).Generate(context.Background(), llm.Request{
		Model: "m",
		Messages: []llm.Message{{
			Role: llm.User,
			Parts: []llm.Part{
				{Type: llm.PartImage, FileID: "file-img", ImageURL: "https://ignored.example/a.png"},
				{Type: llm.PartFile, FileID: "file-doc"},
				{Type: llm.PartImage, ImageURL: "https://example.com/a.png"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"file_id":"file-img"`) || !strings.Contains(body, `"file_id":"file-doc"`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "ignored.example") {
		t.Fatalf("FileID 应优先于 URL: %s", body)
	}
	if !strings.Contains(body, `"image_url"`) || !strings.Contains(body, "https://example.com/a.png") {
		t.Fatalf("普通图片 URL 应保留: %s", body)
	}
}
