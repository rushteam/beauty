package discover

import (
	"context"
	"log/slog"

	"github.com/rushteam/beauty/pkg/logger"
)

type noopRegistry struct{}

func (r noopRegistry) Register(ctx context.Context, info Service) (context.CancelFunc, error) {
	logger.Info("Registering service", slog.String("name", info.Name()))
	return func() {
		logger.Info("Deregistering service", slog.String("name", info.Name()))
	}, nil
}
func NewNoop() Registry {
	return &noopRegistry{}
}
