package otelllm

import (
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/trace"
)

// spanStore 用于 Before/After 之间传递 span 引用。
// Agent Hooks 的 BeforeModel/AfterModel 回调间没有 ctx 传递机制,
// 因此用 step+callID 做 key 在包级临时存储 span。
// 每次 After 取出后立即删除,不会泄露。
var modelSpans sync.Map
var toolSpans sync.Map

func modelSpanKey(step int) string {
	return fmt.Sprintf("model:%d", step)
}

func toolSpanKey(step int, callID string) string {
	return fmt.Sprintf("tool:%d:%s", step, callID)
}

func storeModelSpan(step int, span trace.Span) {
	modelSpans.Store(modelSpanKey(step), span)
}

func loadModelSpan(step int) trace.Span {
	v, ok := modelSpans.LoadAndDelete(modelSpanKey(step))
	if !ok {
		return nil
	}
	return v.(trace.Span)
}

func storeToolSpan(step int, callID string, span trace.Span) {
	toolSpans.Store(toolSpanKey(step, callID), span)
}

func loadToolSpan(step int, callID string) trace.Span {
	v, ok := toolSpans.LoadAndDelete(toolSpanKey(step, callID))
	if !ok {
		return nil
	}
	return v.(trace.Span)
}
