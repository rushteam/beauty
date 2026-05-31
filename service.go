package beauty

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/service/cron"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/pprof"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"google.golang.org/grpc"
)

func WithWebServer(addr string, mux http.Handler, opts ...webserver.Option) Option {
	return WithService(webserver.New(addr, mux, opts...))
}

func WithGrpcServer(addr string, handler func(*grpc.Server), opts ...grpcserver.Option) Option {
	return WithService(grpcserver.New(addr, handler, opts...))
}

func WithCrontab(opts ...cron.CronOptions) Option {
	return WithService(cron.New(opts...))
}

// WithPprof 启动一个独立的 pprof HTTP 服务，默认监听 127.0.0.1:6060。
// 仅在需要线上排查时挂载，生产环境建议通过 SSH 隧道访问而非对外暴露。
func WithPprof(opts ...pprof.Option) Option {
	return WithService(pprof.New(opts...))
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
