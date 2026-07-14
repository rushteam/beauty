package cron

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ScopeName is the instrumentation scope name.
	ScopeName = "github.com/rushteam/beauty/pkg/service/cron/trace.go"
)

// getFunctionName 获取最后两级包路径的函数/方法名
func getFunctionName(i any) string {
	fullName := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()

	// 获取最后一级包名
	parts := strings.Split(fullName, "/")
	relevantPart := parts[len(parts)-1]

	// 如果包含多个点号，说明是结构体方法
	if strings.Count(relevantPart, ".") > 1 {
		// 移除 -fm 后缀（如果存在）
		if strings.HasSuffix(relevantPart, "-fm") {
			relevantPart = relevantPart[:len(relevantPart)-3]
		}
	}

	return relevantPart
}

func wrapCronHandler(cron *Cron, name string, spec string, handler func(ctx context.Context) error) func(ctx context.Context) error {
	metricAttrs := []attribute.KeyValue{
		{
			Key:   "cron_spec",
			Value: attribute.StringValue(spec),
		},
		{
			Key:   "name",
			Value: attribute.StringValue(name),
		},
	}
	metricsAttributeSetOpt := metric.WithAttributeSet(attribute.NewSet(metricAttrs...))
	traceName := "[cron] " + name // 拼一下，不然不好分辨
	return func(ctx context.Context) error {
		ctx, span := cron.tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, trace.SpanContextFromContext(ctx)),
			traceName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("name", name)),
			trace.WithAttributes(attribute.String("cron_spec", spec)),
		)
		// 耗时直接用 time.Now 计算,不依赖 span 实现是否满足 sdktrace.ReadOnlySpan——
		// 未显式配置真实 TracerProvider(beauty.WithTrace())时,全局 tracer 是 noop
		// 实现,产生的 span 不满足该接口,断言会 panic(曾经的 bug:每次任务触发都
		// panic-recover,产生噪音日志与堆栈开销)。
		start := time.Now()
		defer func() {
			span.End()
			timeSpent := time.Since(start)
			cron.metricsJobSpentDuration.Record(ctx, timeSpent.Seconds(), metricsAttributeSetOpt)
		}()

		return handler(ctx)
	}
}
