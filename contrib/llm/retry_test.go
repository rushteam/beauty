package llm

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"
)

// TestRetry_Stream_RetriesSetupError 建流首包错误应重试,耗尽后把最后一次错误 yield 给调用方。
func TestRetry_Stream_RetriesSetupError(t *testing.T) {
	var calls atomic.Int32
	setupErr := errors.New("connection refused")
	c := &namedClient{
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			calls.Add(1)
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{}, setupErr)
			}
		},
	}

	r := Retry(c, 3, time.Millisecond)
	var got error
	var deltas []string
	for chunk, err := range r.Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			got = err
			break
		}
		deltas = append(deltas, chunk.Delta)
	}

	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 retries", calls.Load())
	}
	if !errors.Is(got, setupErr) {
		t.Fatalf("got err = %v, want %v", got, setupErr)
	}
	if len(deltas) != 0 {
		t.Fatalf("unexpected deltas: %v", deltas)
	}
}

// TestRetry_Stream_SetupThenSuccess 前几次建流失败,最终成功时应产出内容且不泄漏中间错误。
func TestRetry_Stream_SetupThenSuccess(t *testing.T) {
	var calls atomic.Int32
	c := &namedClient{
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			n := calls.Add(1)
			return func(yield func(Chunk, error) bool) {
				if n < 3 {
					yield(Chunk{}, errors.New("transient"))
					return
				}
				yield(Chunk{Delta: "ok"}, nil)
			}
		},
	}

	r := Retry(c, 3, time.Millisecond)
	var content string
	for chunk, err := range r.Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		content += chunk.Delta
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
	if content != "ok" {
		t.Fatalf("content = %q, want ok", content)
	}
}

// TestRetry_Stream_NoRetryAfterChunks 一旦产出有效 chunk,中途错误不重试。
func TestRetry_Stream_NoRetryAfterChunks(t *testing.T) {
	var calls atomic.Int32
	midErr := errors.New("mid-stream")
	c := &namedClient{
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			calls.Add(1)
			return func(yield func(Chunk, error) bool) {
				if !yield(Chunk{Delta: "hi"}, nil) {
					return
				}
				yield(Chunk{}, midErr)
			}
		},
	}

	r := Retry(c, 3, time.Millisecond)
	var content string
	var got error
	for chunk, err := range r.Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			got = err
			break
		}
		content += chunk.Delta
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after chunks)", calls.Load())
	}
	if content != "hi" {
		t.Fatalf("content = %q", content)
	}
	if !errors.Is(got, midErr) {
		t.Fatalf("got err = %v, want %v", got, midErr)
	}
}

// TestFallback_Stream_SetupErrorTriesNext 建流失败应切换下一家,不再把错误当 started 吞掉。
func TestFallback_Stream_SetupErrorTriesNext(t *testing.T) {
	setupErr := errors.New("primary down")
	primary := &namedClient{
		name: "primary",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{}, setupErr)
			}
		},
	}
	backup := &namedClient{
		name:    "backup",
		genResp: &Response{Content: "from-backup"},
	}

	var content string
	for chunk, err := range Fallback(primary, backup).Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		content += chunk.Delta
	}
	if content != "from-backup" {
		t.Fatalf("content = %q, want from-backup", content)
	}
}

// TestFallback_Stream_AllFailYieldsLastErr 全部建流失败时,应 yield 最后一次错误(不再静默成功)。
func TestFallback_Stream_AllFailYieldsLastErr(t *testing.T) {
	err1 := errors.New("a failed")
	err2 := errors.New("b failed")
	a := &namedClient{
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) { yield(Chunk{}, err1) }
		},
	}
	b := &namedClient{
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) { yield(Chunk{}, err2) }
		},
	}

	var got error
	for _, err := range Fallback(a, b).Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			got = err
			break
		}
	}
	if !errors.Is(got, err2) {
		t.Fatalf("got err = %v, want %v", got, err2)
	}
}

// TestRetry_Stream_EmptySuccessNoRetry 空流正常结束不应重试。
func TestRetry_Stream_EmptySuccessNoRetry(t *testing.T) {
	var calls atomic.Int32
	c := &namedClient{
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			calls.Add(1)
			return func(yield func(Chunk, error) bool) {} // 零 chunk,无 error
		},
	}

	r := Retry(c, 3, time.Millisecond)
	for _, err := range r.Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (empty success should not retry)", calls.Load())
	}
}

// TestFallback_Stream_EmptySuccessNoSwitch 空流正常结束不应切换备用。
func TestFallback_Stream_EmptySuccessNoSwitch(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	primary := &namedClient{
		name: "primary",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			primaryCalls.Add(1)
			return func(yield func(Chunk, error) bool) {}
		},
	}
	backup := &namedClient{
		name: "backup",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			backupCalls.Add(1)
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{Delta: "should-not-see"}, nil)
			}
		},
	}

	for chunk, err := range Fallback(primary, backup).Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if chunk.Delta != "" {
			t.Fatalf("unexpected delta %q", chunk.Delta)
		}
	}
	if primaryCalls.Load() != 1 || backupCalls.Load() != 0 {
		t.Fatalf("primary=%d backup=%d, want 1/0", primaryCalls.Load(), backupCalls.Load())
	}
}

// TestFallbackConfig_Stream_EmptySuccessNoFallback 空流不应触发 OnError 降级。
func TestFallbackConfig_Stream_EmptySuccessNoFallback(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	primary := &namedClient{
		name: "primary",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			primaryCalls.Add(1)
			return func(yield func(Chunk, error) bool) {}
		},
	}
	backup := &namedClient{
		name: "backup",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			backupCalls.Add(1)
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{Delta: "should-not-see"}, nil)
			}
		},
	}

	c := FallbackConfig{
		Primary: primary,
		OnError: []Client{backup},
	}.Build()

	for chunk, err := range c.Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if chunk.Delta != "" {
			t.Fatalf("unexpected delta %q", chunk.Delta)
		}
	}
	if primaryCalls.Load() != 1 || backupCalls.Load() != 0 {
		t.Fatalf("primary=%d backup=%d, want 1/0", primaryCalls.Load(), backupCalls.Load())
	}
}
