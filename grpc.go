package beauty

import "github.com/rushteam/beauty/pkg/service/grpcserver"

func WithGrpcServer(addr string) Option {
	return func(app *App) {
		app.services = append(app.services, grpcserver.New(addr))
	}
}
