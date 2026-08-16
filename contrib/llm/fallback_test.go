package llm

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
)

type namedClient struct {
	name     string
	genResp  *Response
	genErr   error
	streamFn func(ctx context.Context, req Request) iter.Seq2[Chunk, error]
}

func (c *namedClient) String() string { return c.name }

func (c *namedClient) Generate(_ context.Context, _ Request) (*Response, error) {
	if c.genErr != nil {
		return nil, c.genErr
	}
	return c.genResp, nil
}

func (c *namedClient) Stream(ctx context.Context, req Request) iter.Seq2[Chunk, error] {
	if c.streamFn != nil {
		return c.streamFn(ctx, req)
	}
	return func(yield func(Chunk, error) bool) {
		if c.genErr != nil {
			yield(Chunk{}, c.genErr)
			return
		}
		if c.genResp != nil {
			yield(Chunk{Delta: c.genResp.Content}, nil)
		}
	}
}

type typedErr struct {
	msg  string
	kind ErrorKind
}

func (e *typedErr) Error() string        { return e.msg }
func (e *typedErr) ErrorKind() ErrorKind { return e.kind }

func TestClassifyError(t *testing.T) {
	tests := []struct {
		err  error
		want ErrorKind
	}{
		{nil, ErrorGeneral},
		{errors.New("something went wrong"), ErrorGeneral},
		{errors.New("HTTP 401 unauthorized"), ErrorGeneral},
		{errors.New("rate limit exceeded"), ErrorRateLimit},
		{errors.New("Error 429: Too Many Requests"), ErrorRateLimit},
		{errors.New("429 too many requests"), ErrorRateLimit},
		{errors.New("quota exceeded for model"), ErrorRateLimit},
		{errors.New("request was throttled"), ErrorRateLimit},
		{errors.New("RATE_LIMIT error"), ErrorRateLimit},
		// 独立数字:避免子串误伤
		{errors.New("error code 1429"), ErrorGeneral},
		{errors.New("status 4290"), ErrorGeneral},
		{errors.New("context_length_exceeded"), ErrorContextOverflow},
		{errors.New("maximum context length reached"), ErrorContextOverflow},
		{errors.New("token limit exceeded"), ErrorContextOverflow},
		{errors.New("input too long for model"), ErrorContextOverflow},
		{errors.New("max_tokens exceeded"), ErrorContextOverflow},
		// 参数校验错误不应判为上下文溢出
		{errors.New("max_tokens parameter invalid"), ErrorGeneral},
		{&typedErr{msg: "custom", kind: ErrorRateLimit}, ErrorRateLimit},
		{&typedErr{msg: "custom overflow", kind: ErrorContextOverflow}, ErrorContextOverflow},
	}
	for _, tc := range tests {
		got := ClassifyError(tc.err)
		if got != tc.want {
			t.Errorf("ClassifyError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestFallbackConfig_RateLimit(t *testing.T) {
	primary := &namedClient{name: "primary", genErr: errors.New("rate limit exceeded")}
	backup := &namedClient{name: "rate-backup", genResp: &Response{Content: "from-rate-backup"}}
	general := &namedClient{name: "general-backup", genResp: &Response{Content: "from-general"}}

	c := FallbackConfig{
		Primary:     primary,
		OnRateLimit: []Client{backup},
		OnError:     []Client{general},
	}.Build()

	resp, err := c.Generate(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "from-rate-backup" {
		t.Fatalf("content = %q, want from-rate-backup", resp.Content)
	}
}

func TestFallbackConfig_ContextOverflow(t *testing.T) {
	primary := &namedClient{name: "primary", genErr: errors.New("context_length_exceeded")}
	backup := &namedClient{name: "ctx-backup", genResp: &Response{Content: "from-ctx-backup"}}
	general := &namedClient{name: "general-backup", genResp: &Response{Content: "from-general"}}

	c := FallbackConfig{
		Primary:           primary,
		OnContextOverflow: []Client{backup},
		OnError:           []Client{general},
	}.Build()

	resp, err := c.Generate(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "from-ctx-backup" {
		t.Fatalf("content = %q, want from-ctx-backup", resp.Content)
	}
}

func TestFallbackConfig_GeneralError(t *testing.T) {
	primary := &namedClient{name: "primary", genErr: errors.New("HTTP 500 internal error")}
	rateBackup := &namedClient{name: "rate-backup", genResp: &Response{Content: "rate"}}
	general := &namedClient{name: "general-backup", genResp: &Response{Content: "from-general"}}

	c := FallbackConfig{
		Primary:     primary,
		OnRateLimit: []Client{rateBackup},
		OnError:     []Client{general},
	}.Build()

	resp, err := c.Generate(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "from-general" {
		t.Fatalf("content = %q, want from-general", resp.Content)
	}
}

func TestFallbackConfig_EmptyKindChainFallsThrough(t *testing.T) {
	primary := &namedClient{name: "primary", genErr: errors.New("429 too many requests")}
	general := &namedClient{name: "general-backup", genResp: &Response{Content: "from-general"}}

	c := FallbackConfig{
		Primary: primary,
		OnError: []Client{general},
	}.Build()

	resp, err := c.Generate(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "from-general" {
		t.Fatalf("content = %q, want from-general", resp.Content)
	}
}

func TestFallbackConfig_OnFallbackCallback(t *testing.T) {
	primaryErr := errors.New("rate limit hit")
	primary := &namedClient{name: "primary", genErr: primaryErr}
	backup := &namedClient{name: "backup", genResp: &Response{Content: "ok"}}

	var calls int
	var gotKind ErrorKind
	var gotPrimary, gotFallback string
	var gotErr error

	c := FallbackConfig{
		Primary:     primary,
		OnRateLimit: []Client{backup},
		OnFallback: func(_ context.Context, kind ErrorKind, primary, fallback string, err error) {
			calls++
			gotKind = kind
			gotPrimary = primary
			gotFallback = fallback
			gotErr = err
		},
	}.Build()

	_, err := c.Generate(context.Background(), Request{Model: "m"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if calls != 1 {
		t.Fatalf("OnFallback calls = %d, want 1", calls)
	}
	if gotKind != ErrorRateLimit {
		t.Fatalf("kind = %v, want ErrorRateLimit", gotKind)
	}
	if gotPrimary != "primary" || gotFallback != "backup" {
		t.Fatalf("primary=%q fallback=%q", gotPrimary, gotFallback)
	}
	if gotErr != primaryErr {
		t.Fatalf("err = %v, want %v", gotErr, primaryErr)
	}
}

func TestFallbackConfig_StreamSetupFallback(t *testing.T) {
	setupErr := errors.New("429 rate limit")
	primary := &namedClient{
		name: "primary",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{}, setupErr)
			}
		},
	}
	backup := &namedClient{
		name: "backup",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{Delta: "fallback-stream"}, nil)
			}
		},
	}

	c := FallbackConfig{
		Primary:     primary,
		OnRateLimit: []Client{backup},
	}.Build()

	got, err := collectFallbackStream(t, c.Stream(context.Background(), Request{Model: "m"}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != "fallback-stream" {
		t.Fatalf("stream = %q, want fallback-stream", got)
	}
}

func TestFallbackConfig_StreamNoFallbackAfterChunks(t *testing.T) {
	primary := &namedClient{
		name: "primary",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) {
				if !yield(Chunk{Delta: "partial"}, nil) {
					return
				}
				yield(Chunk{}, errors.New("mid-stream failure"))
			}
		},
	}
	backup := &namedClient{
		name: "backup",
		streamFn: func(_ context.Context, _ Request) iter.Seq2[Chunk, error] {
			return func(yield func(Chunk, error) bool) {
				yield(Chunk{Delta: "should-not-see"}, nil)
			}
		},
	}

	c := FallbackConfig{
		Primary: primary,
		OnError: []Client{backup},
	}.Build()

	var sb strings.Builder
	var streamErr error
	for chunk, err := range c.Stream(context.Background(), Request{Model: "m"}) {
		if err != nil {
			streamErr = err
			break
		}
		sb.WriteString(chunk.Delta)
	}
	if sb.String() != "partial" {
		t.Fatalf("text = %q, want partial", sb.String())
	}
	if streamErr == nil || !strings.Contains(streamErr.Error(), "mid-stream failure") {
		t.Fatalf("expected mid-stream error, got %v", streamErr)
	}
}

func collectFallbackStream(t *testing.T, seq iter.Seq2[Chunk, error]) (string, error) {
	t.Helper()
	var sb strings.Builder
	for chunk, err := range seq {
		if err != nil {
			return sb.String(), err
		}
		sb.WriteString(chunk.Delta)
	}
	return sb.String(), nil
}
