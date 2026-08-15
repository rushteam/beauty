package redact_test

import (
	"context"
	"iter"
	"log/slog"
	"sync"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/redact"
)

type captureStore struct {
	records []slog.Record
	mu      sync.Mutex
}

type captureHandler struct {
	store        *captureStore
	handlerAttrs []slog.Attr
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{store: &captureStore{}}
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	rec := r.Clone()
	for _, a := range h.handlerAttrs {
		rec.AddAttrs(a)
	}
	h.store.records = append(h.store.records, rec)
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{
		store:        h.store,
		handlerAttrs: slicesClone(h.handlerAttrs, attrs...),
	}
}

func (h *captureHandler) WithGroup(_ string) slog.Handler {
	return h
}

func slicesClone[T any](base []T, extra ...T) []T {
	out := make([]T, len(base), len(base)+len(extra))
	copy(out, base)
	return append(out, extra...)
}

func (s *captureStore) attrs() []slog.Attr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) == 0 {
		return nil
	}
	var attrs []slog.Attr
	s.records[len(s.records)-1].Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	return attrs
}

func (s *captureStore) hasKey(key string) bool {
	for _, a := range s.attrs() {
		if a.Key == key {
			return true
		}
	}
	return false
}

func newTestLogger(includeSensitive bool) (*redact.Logger, *captureStore) {
	h := newCaptureHandler()
	return &redact.Logger{
		Inner:            slog.New(h),
		IncludeSensitive: includeSensitive,
	}, h.store
}

func TestSensitiveDataCreatesAttr(t *testing.T) {
	attr := redact.SensitiveData("secret", "value")
	if attr.Key != "secret" {
		t.Fatalf("key = %q, want secret", attr.Key)
	}
	val := attr.Value.Any()
	if val == nil {
		t.Fatal("value is nil")
	}
	if _, ok := val.(slog.LogValuer); !ok {
		t.Fatalf("value type %T does not implement slog.LogValuer", val)
	}
}

func TestLoggerIncludeSensitiveTrue(t *testing.T) {
	logger, h := newTestLogger(true)
	logger.Info(context.Background(), "test",
		"public", "visible",
		redact.SensitiveData("secret", "hidden"),
	)
	if !h.hasKey("public") {
		t.Error("missing public attr")
	}
	if !h.hasKey("secret") {
		t.Error("secret attr should be included when IncludeSensitive=true")
	}
}

func TestLoggerIncludeSensitiveFalseDropsSensitive(t *testing.T) {
	logger, h := newTestLogger(false)
	logger.Info(context.Background(), "test",
		redact.SensitiveData("secret", "hidden"),
	)
	if h.hasKey("secret") {
		t.Error("secret attr should be dropped when IncludeSensitive=false")
	}
}

func TestLoggerIncludeSensitiveFalseKeepsNonSensitive(t *testing.T) {
	logger, h := newTestLogger(false)
	logger.Info(context.Background(), "test",
		"public", "visible",
		redact.SensitiveData("secret", "hidden"),
	)
	if !h.hasKey("public") {
		t.Error("public attr should be kept")
	}
	if h.hasKey("secret") {
		t.Error("secret attr should be dropped")
	}
}

func TestWithPreservesIncludeSensitive(t *testing.T) {
	base, h := newTestLogger(false)
	child := base.With("component", "agent")
	child.Info(context.Background(), "test", redact.SensitiveData("secret", "x"))
	if h.hasKey("secret") {
		t.Error("child logger should inherit IncludeSensitive=false")
	}
	if !h.hasKey("component") {
		t.Error("With attrs should be present")
	}
}

func TestFilterMixedAttrAndKeyValue(t *testing.T) {
	logger, h := newTestLogger(false)
	logger.Info(context.Background(), "mixed",
		"normal_kv", "ok",
		redact.SensitiveData("attr_secret", "drop-me"),
		"secret_kv", sensitiveDataValue("drop-me-too"),
		"another", 42,
	)
	keys := map[string]bool{}
	for _, a := range h.attrs() {
		keys[a.Key] = true
	}
	want := map[string]bool{"normal_kv": true, "another": true}
	for k := range want {
		if !keys[k] {
			t.Errorf("missing expected key %q", k)
		}
	}
	for _, drop := range []string{"attr_secret", "secret_kv"} {
		if keys[drop] {
			t.Errorf("sensitive key %q should be dropped", drop)
		}
	}
}

// sensitiveDataValue 模拟直接以 key-value 形式传入 sensitiveData 的场景。
func sensitiveDataValue(v any) any {
	attr := redact.SensitiveData("_", v)
	return attr.Value.Any()
}

func TestRedactedLoggingMiddleware(t *testing.T) {
	logger, store := newTestLogger(false)

	core := agent.AgentRunFunc(func(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
		return func(yield func(agent.Event, error) bool) {
			yield(agent.Event{Type: agent.EventFinal, Response: &llm.Response{
				Content: "done",
				Usage:   llm.Usage{InputTokens: 10, OutputTokens: 5},
			}}, nil)
		}
	})

	fn := redact.RedactedLoggingMiddleware(logger)(core)
	msgs := []llm.Message{{Role: llm.User, Content: "hello"}}
	for range fn(context.Background(), llm.Request{Model: "test", Messages: msgs}) {
	}

	if len(store.records) != 2 {
		t.Fatalf("records = %d, want 2 (start + done)", len(store.records))
	}
	startAttrs := attrsAt(store, 0)
	if !hasAttr(startAttrs, "model", "test") {
		t.Error("start log missing model")
	}
	if !hasAttr(startAttrs, "messages", 1) {
		t.Error("start log missing message count")
	}
	if hasAttrKey(startAttrs, "messages_content") {
		t.Error("messages_content should be redacted")
	}

	doneAttrs := attrsAt(store, 1)
	if !hasAttr(doneAttrs, "content_len", 4) {
		t.Error("done log missing content_len")
	}
	if !hasAttr(doneAttrs, "input_tokens", 10) {
		t.Error("done log missing input_tokens")
	}
}

func attrsAt(s *captureStore, idx int) []slog.Attr {
	s.mu.Lock()
	defer s.mu.Unlock()
	var attrs []slog.Attr
	s.records[idx].Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	return attrs
}

func hasAttrKey(attrs []slog.Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

func hasAttr(attrs []slog.Attr, key string, want any) bool {
	for _, a := range attrs {
		if a.Key != key {
			continue
		}
		got := a.Value.Any()
		switch w := want.(type) {
		case int:
			if g, ok := got.(int64); ok && int(g) == w {
				return true
			}
			if g, ok := got.(int); ok && g == w {
				return true
			}
		default:
			if got == want {
				return true
			}
		}
	}
	return false
}
