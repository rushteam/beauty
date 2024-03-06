package discover

import (
	"github.com/rushteam/beauty/pkg/logger"
)

type EmptyRegistry struct{}

func (r EmptyRegistry) Register(info Endpoint) error {
	logger.Info("Registering service %s", info.ServiceName())
	return nil
}

func (r EmptyRegistry) Deregister(info Endpoint) error {
	logger.Info("Deregistering service %s", info.ServiceName())
	return nil
}
