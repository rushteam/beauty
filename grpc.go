package beauty

import (
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"google.golang.org/grpc"
)

func WithGrpcServer(addr string, handler func(*grpc.Server)) Option {
	return func(app *App) {
		app.services = append(app.services, grpcserver.New(addr, handler))
	}
}
