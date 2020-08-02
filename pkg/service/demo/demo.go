package demo

import (
	"context"
	"time"

	"github.com/rushteam/mojito"
	"github.com/rushteam/mojito/pkg/registry"
)

// New ..
func New() mojito.Service {
	return &Demo{
		service: registry.Service{
			Namespace: "defalut",
			Kind:      "demo",
			Name:      "demo",
		},
	}
}

// Demo is a demo service
type Demo struct {
	service registry.Service
	closed  chan struct{}
}

// Start ..
func (d *Demo) Start() error {
	d.closed = make(chan struct{})
	select {
	case <-d.closed:
		time.Sleep(time.Second * 8)
		return nil
		// case <-time.After(time.Second * 15):
		// 	return nil
	}
}

// Close ..
func (d *Demo) Close(ctx context.Context) error {
	// time.Sleep(time.Second * 8)
	close(d.closed)
	return nil
}

//Service ..
func (d *Demo) Service() registry.Service {
	return d.service
}
