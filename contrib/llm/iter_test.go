package llm_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

type stubClient struct {
	content string
	chunks  []string
	err     error
}

func (s *stubClient) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &llm.Response{Content: s.content, Model: "test"}, nil
}

func (s *stubClient) Stream(_ context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		if s.err != nil {
			yield(llm.Chunk{}, s.err)
			return
		}
		for _, c := range s.chunks {
			if !yield(llm.Chunk{Delta: c}, nil) {
				return
			}
		}
		yield(llm.Chunk{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 20}}, nil)
	}
}

func TestStream(t *testing.T) {
	c := &stubClient{content: "hello", chunks: []string{"he", "ll", "o"}}

	resp, err := c.Generate(context.Background(), llm.Request{})
	if err != nil || resp.Content != "hello" {
		t.Errorf("Generate = %v, %v; want 'hello', nil", resp, err)
	}

	var collected string
	for chunk, err := range c.Stream(context.Background(), llm.Request{}) {
		if err != nil {
			t.Fatalf("Stream error: %v", err)
		}
		collected += chunk.Delta
	}
	if collected != "hello" {
		t.Errorf("Stream collected = %q, want 'hello'", collected)
	}
}

func TestCollect(t *testing.T) {
	c := &stubClient{chunks: []string{"a", "b", "c"}}
	resp, err := llm.Collect(c.Stream(context.Background(), llm.Request{}))
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}
	if resp.Content != "abc" {
		t.Errorf("Collect content = %q, want 'abc'", resp.Content)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 20 {
		t.Errorf("Collect usage = %+v", resp.Usage)
	}
}

func TestStreamError(t *testing.T) {
	errTest := errors.New("test error")
	c := &stubClient{err: errTest}

	for _, err := range c.Stream(context.Background(), llm.Request{}) {
		if err != nil {
			if !errors.Is(err, errTest) {
				t.Errorf("expected test error, got %v", err)
			}
			return
		}
	}
	t.Error("expected error from Stream")
}
