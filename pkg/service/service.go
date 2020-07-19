package service

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/rushteam/mojito"
)

var _ mojito.ServiceOptions = (*Options)(nil)

// Options ..
type Options struct {
	kind     string
	name     string
	uuid     string
	version  string
	metadata map[string]string
}

//OptionsFunc ..
type OptionsFunc func(o *Options)

//NewOptions new a options
func NewOptions(kind string, opts ...OptionsFunc) *Options {
	o := &Options{
		kind:     kind,
		name:     kind,
		uuid:     uuid.New().String(),
		metadata: make(map[string]string),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

//Name ..
func Name(name string) OptionsFunc {
	return func(o *Options) {
		o.name = name
	}
}

//Version ..
func Version(verison string) OptionsFunc {
	return func(o *Options) {
		o.version = verison
	}
}

//Name ...
func (o *Options) Name() string {
	return fmt.Sprintf("%v-%v", o.kind, o.name)
}

//Version ...
func (o *Options) Version() string {
	return o.version
}

//Metadata ...
func (o *Options) Metadata() map[string]string {
	return o.metadata
}
