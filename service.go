package beauty

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/service/cron"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"google.golang.org/grpc"
)

func WithWebServer(addr string, mux http.Handler) Option {
	return WithService(webserver.New(addr, mux))
}

func WithGrpcServer(addr string, handler func(*grpc.Server)) Option {
	return WithService(grpcserver.New(addr, handler))
}

func WithCrontab(opts ...cron.CronOptions) Option {
	return WithService(cron.New(opts...))
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
