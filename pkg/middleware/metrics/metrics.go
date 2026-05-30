package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var histogramBoundaries = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

type httpMetrics struct {
	reqCount    metric.Int64Counter
	reqDuration metric.Float64Histogram
	reqInflight metric.Int64UpDownCounter
}

type grpcMetrics struct {
	reqCount    metric.Int64Counter
	reqDuration metric.Float64Histogram
	reqInflight metric.Int64UpDownCounter
}

var (
	httpOnce  sync.Once
	httpM     *httpMetrics
	httpMErr  error
	grpcOnce  sync.Once
	grpcM     *grpcMetrics
	grpcMErr  error
)

func initHTTPMetrics(serviceName string) (*httpMetrics, error) {
	httpOnce.Do(func() {
		m := otel.GetMeterProvider().Meter(serviceName)
		bOpt := metric.WithExplicitBucketBoundaries(histogramBoundaries...)

		count, err := m.Int64Counter(
			"http.server.request.count",
			metric.WithDescription("Total number of HTTP requests"),
		)
		if err != nil {
			httpMErr = err
			return
		}
		dur, err := m.Float64Histogram(
			"http.server.request.duration",
			metric.WithDescription("HTTP request duration in milliseconds"),
			metric.WithUnit("ms"),
			bOpt,
		)
		if err != nil {
			httpMErr = err
			return
		}
		inflight, err := m.Int64UpDownCounter(
			"http.server.request.inflight",
			metric.WithDescription("Number of in-flight HTTP requests"),
		)
		if err != nil {
			httpMErr = err
			return
		}
		httpM = &httpMetrics{
			reqCount:    count,
			reqDuration: dur,
			reqInflight: inflight,
		}
	})
	return httpM, httpMErr
}

func initGRPCMetrics(serviceName string) (*grpcMetrics, error) {
	grpcOnce.Do(func() {
		m := otel.GetMeterProvider().Meter(serviceName)
		bOpt := metric.WithExplicitBucketBoundaries(histogramBoundaries...)

		count, err := m.Int64Counter(
			"grpc.server.request.count",
			metric.WithDescription("Total number of gRPC requests"),
		)
		if err != nil {
			grpcMErr = err
			return
		}
		dur, err := m.Float64Histogram(
			"grpc.server.request.duration",
			metric.WithDescription("gRPC request duration in milliseconds"),
			metric.WithUnit("ms"),
			bOpt,
		)
		if err != nil {
			grpcMErr = err
			return
		}
		inflight, err := m.Int64UpDownCounter(
			"grpc.server.request.inflight",
			metric.WithDescription("Number of in-flight gRPC requests"),
		)
		if err != nil {
			grpcMErr = err
			return
		}
		grpcM = &grpcMetrics{
			reqCount:    count,
			reqDuration: dur,
			reqInflight: inflight,
		}
	})
	return grpcM, grpcMErr
}

func normalizePath(raw string) string {
	path, _, _ := strings.Cut(raw, "?")
	if len(path) > 100 {
		return "long_path"
	}
	return path
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func HTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		m, err := initHTTPMetrics(serviceName)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err != nil || m == nil {
				next.ServeHTTP(w, r)
				return
			}

			method := r.Method
			path := normalizePath(r.URL.RequestURI())
			ctx := r.Context()

			inflightAttrs := metric.WithAttributes(
				attribute.String("method", method),
			)
			m.reqInflight.Add(ctx, 1, inflightAttrs)
			defer m.reqInflight.Add(ctx, -1, inflightAttrs)

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rw, r)
			elapsed := float64(time.Since(start).Milliseconds())

			attrs := metric.WithAttributes(
				attribute.String("method", method),
				attribute.String("path", path),
				attribute.String("status_code", fmt.Sprintf("%d", rw.statusCode)),
			)
			m.reqCount.Add(ctx, 1, attrs)
			m.reqDuration.Record(ctx, elapsed, attrs)
		})
	}
}

func UnaryServerInterceptor(serviceName string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		m, err := initGRPCMetrics(serviceName)
		if err != nil || m == nil {
			return handler(ctx, req)
		}

		method := info.FullMethod
		inflightAttrs := metric.WithAttributes(
			attribute.String("method", method),
		)
		m.reqInflight.Add(ctx, 1, inflightAttrs)
		defer m.reqInflight.Add(ctx, -1, inflightAttrs)

		start := time.Now()
		resp, handlerErr := handler(ctx, req)
		elapsed := float64(time.Since(start).Milliseconds())

		code := status.Code(handlerErr).String()
		attrs := metric.WithAttributes(
			attribute.String("method", method),
			attribute.String("status_code", code),
		)
		m.reqCount.Add(ctx, 1, attrs)
		m.reqDuration.Record(ctx, elapsed, attrs)

		return resp, handlerErr
	}
}

func StreamServerInterceptor(serviceName string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		m, err := initGRPCMetrics(serviceName)
		if err != nil || m == nil {
			return handler(srv, ss)
		}

		ctx := ss.Context()
		method := info.FullMethod
		inflightAttrs := metric.WithAttributes(
			attribute.String("method", method),
		)
		m.reqInflight.Add(ctx, 1, inflightAttrs)
		defer m.reqInflight.Add(ctx, -1, inflightAttrs)

		start := time.Now()
		handlerErr := handler(srv, ss)
		elapsed := float64(time.Since(start).Milliseconds())

		code := status.Code(handlerErr).String()
		attrs := metric.WithAttributes(
			attribute.String("method", method),
			attribute.String("status_code", code),
		)
		m.reqCount.Add(ctx, 1, attrs)
		m.reqDuration.Record(ctx, elapsed, attrs)

		return handlerErr
	}
}
