package beauty

import (
	"context"
	"net"
	"net/http"

	"github.com/rushteam/beauty/pkg/service/console"
	"github.com/rushteam/beauty/pkg/service/cron"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/pprof"
	"github.com/rushteam/beauty/pkg/service/tcpserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"google.golang.org/grpc"
)

func WithWebServer(addr string, mux http.Handler, opts ...webserver.Option) Option {
	return WithService(webserver.New(addr, mux, opts...))
}

func WithGrpcServer(addr string, handler func(*grpc.Server), opts ...grpcserver.Option) Option {
	return WithService(grpcserver.New(addr, handler, opts...))
}

// WithTcpServer 启动一个原生 TCP 服务。handler 处理每个接入的连接。
// 适合自定义二进制协议的场景(IoT 设备接入、游戏网关等)。
func WithTcpServer(addr string, handler func(ctx context.Context, conn net.Conn), opts ...tcpserver.Option) Option {
	return WithService(tcpserver.New(addr, handler, opts...))
}

func WithCrontab(opts ...cron.CronOptions) Option {
	return WithService(cron.New(opts...))
}

// WithPprof 启动一个独立的 pprof HTTP 服务，默认监听 127.0.0.1:6060。
// 仅在需要线上排查时挂载，生产环境建议通过 SSH 隧道访问而非对外暴露。
func WithPprof(opts ...pprof.Option) Option {
	return WithService(pprof.New(opts...))
}

// WithConsole 启动一个远程 Web 控制台服务，默认监听 127.0.0.1:6070。
// 浏览器打开 http://<addr>/console 即可交互式执行命令（内置 env/mem/gc/goroutine/deadlock
// 等排障命令，支持自定义命令、Tab 补全、历史重放与 Topic 周期推送）。
// 生产环境务必配置账号（console.WithUser）或通过反向代理鉴权，避免对外暴露。
func WithConsole(opts ...console.Option) Option {
	return WithService(console.New(opts...))
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
