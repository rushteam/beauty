package otelllm

import (
	"context"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func TestMeteredWithErrors_Generate_Success(t *testing.T) {
	var reports []UsageReport

	fake := &fakeClient{
		resp: &llm.Response{
			Model: "gpt-4",
			Usage: llm.Usage{InputTokens: 100, OutputTokens: 50},
		},
	}

	client := MeteredWithErrors(fake, func(_ context.Context, r UsageReport) {
		reports = append(reports, r)
	}, "openai")

	resp, err := client.Generate(context.Background(), llm.Request{Model: "gpt-4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != "gpt-4" {
		t.Errorf("unexpected model: %s", resp.Model)
	}

	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	r := reports[0]
	if r.Err != nil {
		t.Errorf("expected no error, got %v", r.Err)
	}
	if r.Model != "gpt-4" {
		t.Errorf("expected model 'gpt-4', got %q", r.Model)
	}
	if r.Usage.InputTokens != 100 || r.Usage.OutputTokens != 50 {
		t.Errorf("unexpected usage: %+v", r.Usage)
	}
	if r.System != "openai" {
		t.Errorf("expected system 'openai', got %q", r.System)
	}
}

func TestMeteredWithErrors_Generate_Error(t *testing.T) {
	var reports []UsageReport

	fake := &fakeClient{err: errors.New("rate limited")}
	client := MeteredWithErrors(fake, func(_ context.Context, r UsageReport) {
		reports = append(reports, r)
	}, "openai")

	_, err := client.Generate(context.Background(), llm.Request{Model: "gpt-4"})
	if err == nil {
		t.Fatal("expected error")
	}

	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Err == nil {
		t.Error("expected error in report")
	}
}

func TestMeteredWithErrors_Stream_Success(t *testing.T) {
	var reports []UsageReport

	fake := &fakeClient{
		resp: &llm.Response{
			Model: "gpt-4o",
			Usage: llm.Usage{InputTokens: 200, OutputTokens: 80},
		},
	}

	client := MeteredWithErrors(fake, func(_ context.Context, r UsageReport) {
		reports = append(reports, r)
	}, "openai")

	for _, err := range client.Stream(context.Background(), llm.Request{Model: "gpt-4o"}) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}

	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	r := reports[0]
	if r.Err != nil {
		t.Errorf("expected no error, got %v", r.Err)
	}
	if !r.Stream {
		t.Error("expected stream=true")
	}
	if r.Usage.InputTokens != 200 || r.Usage.OutputTokens != 80 {
		t.Errorf("unexpected usage: %+v", r.Usage)
	}
}

func TestMeteredWithErrors_Stream_Error(t *testing.T) {
	var reports []UsageReport

	fake := &fakeClient{err: errors.New("connection refused")}
	client := MeteredWithErrors(fake, func(_ context.Context, r UsageReport) {
		reports = append(reports, r)
	}, "anthropic")

	for _, err := range client.Stream(context.Background(), llm.Request{Model: "claude-3"}) {
		if err == nil {
			t.Fatal("expected error from stream")
		}
	}

	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Err == nil {
		t.Error("expected error in report")
	}
	if !reports[0].Stream {
		t.Error("expected stream=true")
	}
}

func TestOTelUsageHook(t *testing.T) {
	hook := OTelUsageHook()
	// 确保不 panic
	hook(context.Background(), UsageReport{
		Model:  "gpt-4",
		Usage:  llm.Usage{InputTokens: 100, OutputTokens: 50},
		System: "openai",
	})
	hook(context.Background(), UsageReport{
		Model:  "gpt-4",
		Err:    errors.New("rate limited"),
		System: "openai",
	})
}
