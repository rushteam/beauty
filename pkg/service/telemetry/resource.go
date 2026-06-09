package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// buildResource 构造带 service.name 的 OTel Resource，并与 SDK 默认 Resource 合并。
//
// 用 resource.Merge(resource.Default(), ...) 而非直接 NewWithAttributes，是为了保留：
//   - SDK 默认属性（telemetry.sdk.name/version/language 等）；
//   - OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES 环境变量（resource.Default 会读取）。
//
// 显式传入的 serviceName 会覆盖环境变量里的 service.name（后者作为 a、前者作为 b，b 优先）。
//
// 自定义部分用 NewSchemaless（空 schema URL），可与任意 schema 的 Default() 干净合并，
// 避免 semconv 版本不一致时 Merge 因 schema URL 冲突而报错。
func buildResource(serviceName string, extra ...attribute.KeyValue) *resource.Resource {
	attrs := append([]attribute.KeyValue{semconv.ServiceName(serviceName)}, extra...)
	merged, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		// Merge 仅在 schema URL 冲突时报错；NewSchemaless 无 schema，正常不会触发。
		// 兜底返回纯自定义 Resource，保证 service.name 始终生效。
		return resource.NewSchemaless(attrs...)
	}
	return merged
}

// WithTraceServiceName 为 trace 设置 service.name（外加可选的额外资源属性，如版本、环境）。
// 不设置 Resource 时，Collector 里的 span 缺少 service.name、无法区分来源服务。
//
//	WithTraceServiceName("order-api",
//		semconv.ServiceVersion("v1.2.0"),
//		semconv.DeploymentEnvironmentName("prod"),
//	)
func WithTraceServiceName(serviceName string, extra ...attribute.KeyValue) TraceOption {
	return WithTraceProviderOption(sdktrace.WithResource(buildResource(serviceName, extra...)))
}

// WithMetricServiceName 为 metric 设置 service.name（外加可选的额外资源属性）。
// 与 WithTraceServiceName 对应，建议同一服务两处传相同的 serviceName。
func WithMetricServiceName(serviceName string, extra ...attribute.KeyValue) MetricOption {
	return WithMetricOption(sdkmetric.WithResource(buildResource(serviceName, extra...)))
}
