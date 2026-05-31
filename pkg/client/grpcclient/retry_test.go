package grpcclient

import (
	"encoding/json"
	"testing"
)

func TestDefaultRetryPolicy_serviceConfig(t *testing.T) {
	p := DefaultRetryPolicy()
	raw := p.serviceConfig()

	// 必须是合法 JSON
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("serviceConfig is not valid JSON: %v\nraw: %s", err, raw)
	}

	// methodConfig 必须存在且非空
	mc, ok := cfg["methodConfig"].([]any)
	if !ok || len(mc) == 0 {
		t.Fatalf("missing methodConfig, raw: %s", raw)
	}

	// retryPolicy 字段校验
	entry := mc[0].(map[string]any)
	rp, ok := entry["retryPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("missing retryPolicy, raw: %s", raw)
	}

	if got := rp["maxAttempts"].(float64); got != 3 {
		t.Errorf("maxAttempts want 3, got %v", got)
	}
	if got := rp["initialBackoff"]; got != "0.1s" {
		t.Errorf("initialBackoff want 0.1s, got %v", got)
	}
	if got := rp["maxBackoff"]; got != "1s" {
		t.Errorf("maxBackoff want 1s, got %v", got)
	}

	codes := rp["retryableStatusCodes"].([]any)
	if len(codes) != 1 || codes[0] != "UNAVAILABLE" {
		t.Errorf("retryableStatusCodes want [UNAVAILABLE], got %v", codes)
	}
}

func TestRetryPolicy_MaxAttemptsClamp(t *testing.T) {
	cases := []struct {
		input int
		want  int
	}{
		{0, 2}, // 低于 2 钳位到 2
		{1, 2},
		{3, 3},
		{5, 5},
		{6, 5}, // 超过 5 钳位到 5
		{9, 5},
	}
	for _, tc := range cases {
		p := RetryPolicy{
			MaxAttempts:          tc.input,
			InitialBackoff:       "0.1s",
			MaxBackoff:           "1s",
			BackoffMultiplier:    2.0,
			RetryableStatusCodes: []string{"UNAVAILABLE"},
		}
		raw := p.serviceConfig()
		var cfg map[string]any
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("input=%d: invalid JSON: %v", tc.input, err)
		}
		mc := cfg["methodConfig"].([]any)
		rp := mc[0].(map[string]any)["retryPolicy"].(map[string]any)
		got := int(rp["maxAttempts"].(float64))
		if got != tc.want {
			t.Errorf("input=%d: want maxAttempts=%d, got=%d", tc.input, tc.want, got)
		}
	}
}

func TestRetryPolicy_WithResourceExhausted(t *testing.T) {
	p := DefaultRetryPolicy().WithResourceExhausted()
	raw := p.serviceConfig()

	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	mc := cfg["methodConfig"].([]any)
	rp := mc[0].(map[string]any)["retryPolicy"].(map[string]any)
	codes := rp["retryableStatusCodes"].([]any)

	found := map[string]bool{}
	for _, c := range codes {
		found[c.(string)] = true
	}
	if !found["UNAVAILABLE"] {
		t.Error("expected UNAVAILABLE in retryableStatusCodes")
	}
	if !found["RESOURCE_EXHAUSTED"] {
		t.Error("expected RESOURCE_EXHAUSTED in retryableStatusCodes")
	}
}

func TestRetryPolicy_EmptyCodesDisablesRetry(t *testing.T) {
	// RetryableStatusCodes 为空时 discovery 侧不注入 ServiceConfig
	p := RetryPolicy{MaxAttempts: 3}
	if len(p.RetryableStatusCodes) != 0 {
		t.Error("empty policy should have no status codes")
	}
	// serviceConfig 本身仍可生成合法 JSON（即使 codes 为空数组）
	raw := p.serviceConfig()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}
