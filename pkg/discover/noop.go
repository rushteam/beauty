package discover

import (
	"context"
	"log/slog"

	"github.com/rushteam/beauty/pkg/logger"
)

type noopRegistry struct{}

func (r noopRegistry) Register(ctx context.Context, info Service) error {
	logger.Info("Registering service", slog.String("name", info.Name()))
	return nil
}

func (r noopRegistry) Deregister(ctx context.Context, info Service) error {
	logger.Info("Deregistering service", slog.String("name", info.Name()))
	return nil
}

func NewNoop() Registry {
	return &noopRegistry{}
}
