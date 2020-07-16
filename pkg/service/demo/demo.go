package demo

import (
	"context"
	"log"
	"time"

	"github.com/rushteam/mojito"
	"github.com/rushteam/mojito/pkg/service"
)

// New ..
func New(opts ...service.OptionsFunc) mojito.Service {
	return &Demo{
		opts: service.NewOptions(opts...),
	}
}

// Demo is a demo service
type Demo struct {
	opts   mojito.ServiceOptions
	closed chan struct{}
}

// Start ..
func (d *Demo) Start() error {
	log.Println("demo start")
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

//Options ..
func (d *Demo) Options() mojito.ServiceOptions {
	return d.opts
}
