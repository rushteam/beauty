package discover

import (
	"context"

	"github.com/rushteam/beauty/pkg/logger"
)

type noopRegistry struct{}

func (r noopRegistry) Register(ctx context.Context, info Service) error {
	logger.Info("Registering service %s", info.Name())
	return nil
}

func (r noopRegistry) Deregister(ctx context.Context, info Service) error {
	logger.Info("Deregistering service %s", info.Name())
	return nil
}

func NewNoop() Registry {
	return &noopRegistry{}
}
