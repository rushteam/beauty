package demo

import (
	"context"
	"time"

	"github.com/rushteam/mojito"
)

// New ..
func New(opts ...mojito.OptionsFunc) mojito.Service {
	return &Demo{
		opts: mojito.NewOptions("demo", opts...),
	}
}

// Demo is a demo service
type Demo struct {
	opts   *mojito.Options
	closed chan struct{}
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

//Options ..
func (d *Demo) Options() *mojito.Options {
	return d.opts
}
