package discover

import "context"

type Registry interface {
	Register(context.Context, Service) (context.CancelFunc, error)
}
