package demo

import (
	"log"
	"time"

	"github.com/nats-io/nuid"
	"github.com/rushteam/mojito"
)

// New ..
func New() mojito.Service {
	n := nuid.New()
	return &Demo{
		opts: &Options{
			name: "demo",
			uuid: n.Next(),
		},
	}
}

// Options ..
type Options struct {
	uuid string
	name string
}

//ID ...
func (o *Options) ID() string {
	return o.name + "-" + o.uuid
}

//Name ...
func (o *Options) Name() string {
	return o.name
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
		time.Sleep(time.Second * 1)
		return nil
		// case <-time.After(time.Second * 15):
		// 	return nil
	}
}

// Close ..
func (d *Demo) Close() error {
	close(d.closed)
	return nil
}

//Options ..
func (d *Demo) Options() mojito.ServiceOptions {
	return d.opts
}
