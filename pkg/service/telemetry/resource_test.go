package telemetry

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func TestBuildResource(t *testing.T) {
	res := buildResource("order-api", semconv.ServiceVersion("v1.2.0"))

	got := map[attribute.Key]string{}
	for _, kv := range res.Attributes() {
		got[kv.Key] = kv.Value.AsString()
	}

	if got[semconv.ServiceNameKey] != "order-api" {
		t.Errorf("service.name = %q, want order-api", got[semconv.ServiceNameKey])
	}
	if got[semconv.ServiceVersionKey] != "v1.2.0" {
		t.Errorf("service.version = %q, want v1.2.0", got[semconv.ServiceVersionKey])
	}
	// 合并保留了 SDK 默认属性（telemetry.sdk.* 由 resource.Default 提供）。
	if _, ok := got["telemetry.sdk.name"]; !ok {
		t.Error("expected telemetry.sdk.name from merged default resource, missing")
	}
}

// 显式 serviceName 应覆盖默认 Resource 中的 service.name（b 优先于 a）。
func TestBuildResourceOverridesDefaultServiceName(t *testing.T) {
	res := buildResource("explicit-name")
	for _, kv := range res.Attributes() {
		if kv.Key == semconv.ServiceNameKey && kv.Value.AsString() != "explicit-name" {
			t.Errorf("service.name = %q, want explicit-name (explicit should win)", kv.Value.AsString())
		}
	}
}
