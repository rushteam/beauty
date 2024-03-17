package discover

import "context"

type Registry interface {
	Register(context.Context, Service) error
	Deregister(context.Context, Service) error
}
