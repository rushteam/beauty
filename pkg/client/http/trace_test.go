package resty_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	httpclient "github.com/rushteam/beauty/pkg/client/http"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestNewHTTPClient_PropagatesTrace 验证 NewHTTPClient 发出的请求会携带 traceparent header。
func TestNewHTTPClient_PropagatesTrace(t *testing.T) {
	// 初始化内存 exporter 和 propagator
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// 目标服务器，记录收到的 traceparent
	var receivedTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 用 tracer 建一个父 span，模拟 A 服务内部已有 trace
	ctx, span := tp.Tracer("test").Start(t.Context(), "parent-span")
	defer span.End()

	// 用 NewHTTPClient 发请求
	client := httpclient.NewHTTPClient()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if receivedTraceparent == "" {
		t.Error("downstream server did not receive traceparent header — trace would be broken")
	}
	t.Logf("traceparent propagated: %s", receivedTraceparent)
}

// TestHTTPDefaultClient_DoesNotPropagate 对照组：标准 http.DefaultClient 不传播 trace。
func TestHTTPDefaultClient_DoesNotPropagate(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))

	var receivedTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("test").Start(t.Context(), "parent-span")
	defer span.End()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if receivedTraceparent != "" {
		t.Errorf("http.DefaultClient should NOT propagate trace, but got: %s", receivedTraceparent)
	}
}
