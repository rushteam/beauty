package demo

import (
	"context"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/registry"
)

// New ..
func New() beauty.Service {
	return &Demo{
		service: &registry.Service{
			Namespace: "defalut",
			Kind:      "demo",
			Name:      "demo",
		},
	}
}

// Demo is a demo service
type Demo struct {
	service *registry.Service
	closed  chan struct{}
}

// Start ..
func (d *Demo) Start(ctx context.Context) error {
	d.closed = make(chan struct{})
	select {
	case <-d.closed:
		time.Sleep(time.Second * 8)
		return nil
		// case <-time.After(time.Second * 15):
		// 	return nil
	}
}

// Stop ..
func (d *Demo) Stop(ctx context.Context) error {
	// time.Sleep(time.Second * 8)
	close(d.closed)
	return nil
}

//Service ..
func (d *Demo) Service() *registry.Service {
	return d.service
}
