package mojito

import (
	"context"

	"github.com/rushteam/mojito/pkg/registry"
)

// var _ registry.Service = (*Options)(nil)

//Service ..
type Service interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Service() *registry.Service
}
