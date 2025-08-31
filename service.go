package beauty

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/circuitbreaker"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/service/cron"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"google.golang.org/grpc"
)

func WithWebServer(addr string, mux http.Handler, opts ...ServiceOption) Option {
	si := &discover.ServiceInfo{
		Metadata: make(map[string]string),
	}
	for _, o := range opts {
		o(si)
	}
	return WithService(webserver.New(
		addr,
		mux,
		webserver.WithServiceName(si.Name),
		webserver.WithMetadata(si.Metadata),
	))
}

func WithGrpcServer(addr string, handler func(*grpc.Server), opts ...ServiceOption) Option {
	si := &discover.ServiceInfo{
		Metadata: make(map[string]string),
	}
	for _, o := range opts {
		o(si)
	}
	return WithService(grpcserver.New(
		addr,
		handler,
		grpcserver.WithServiceName(si.Name),
		grpcserver.WithMetadata(si.Metadata),
	))
}

func WithCrontab(opts ...cron.CronOptions) Option {
	return WithService(cron.New(opts...))
}

// WithWebServerCircuitBreaker 为 Web 服务器添加熔断器
func WithWebServerCircuitBreaker(addr string, mux http.Handler, cb *circuitbreaker.CircuitBreaker, opts ...ServiceOption) Option {
	si := &discover.ServiceInfo{
		Metadata: make(map[string]string),
	}
	for _, o := range opts {
		o(si)
	}
	return WithService(webserver.New(
		addr,
		mux,
		webserver.WithServiceName(si.Name),
		webserver.WithMetadata(si.Metadata),
		webserver.WithCircuitBreaker(cb),
	))
}

// WithGrpcServerCircuitBreaker 为 gRPC 服务器添加熔断器
func WithGrpcServerCircuitBreaker(addr string, handler func(*grpc.Server), cb *circuitbreaker.CircuitBreaker, opts ...ServiceOption) Option {
	si := &discover.ServiceInfo{
		Metadata: make(map[string]string),
	}
	for _, o := range opts {
		o(si)
	}
	return WithService(grpcserver.New(
		addr,
		handler,
		grpcserver.WithServiceName(si.Name),
		grpcserver.WithMetadata(si.Metadata),
		grpcserver.WithCircuitBreaker(cb),
	))
}

// var WebLogger = middleware.Logger
// var WebRecoverer = middleware.Recoverer

// var DefaultMiddlewares = []func(next http.Handler) http.Handler{
// 	middleware.Logger,
// 	middleware.Recoverer,
// }

/*
	type Route struct {
		Method  string
		URI     string
		Handler http.HandlerFunc
	}

type RouteOption func(r *chi.Mux)

	func WithWebServerChi(addr string, routes []Route, opts ...RouteOption) Option {
		r := chi.NewRouter()
		for _, v := range opts {
			v(r)
		}
		for _, v := range routes {
			if v.Method == "" {
				v.Method = http.MethodGet
			}
			r.Method(v.Method, v.URI, v.Handler)
		}
		return WithWebServer(addr, r)
	}

	func WithChiMiddleware(middlewares ...func(http.Handler) http.Handler) RouteOption {
		return func(r *chi.Mux) {
			for _, v := range middlewares {
				r.Use(v)
			}
		}
	}
*/
