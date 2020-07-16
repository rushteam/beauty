package service

import (
	"github.com/google/uuid"
	"github.com/rushteam/mojito"
)

var _ mojito.ServiceOptions = (*Options)(nil)

// Options ..
type Options struct {
	name     string
	version  string
	metadata map[string]string
	uuid     string
}

//OptionsFunc ..
type OptionsFunc func(o *Options)

//NewOptions new a options
func NewOptions(opts ...OptionsFunc) *Options {
	o := &Options{
		version:  "0.0.0",
		metadata: make(map[string]string),
		uuid:     uuid.New().String(),
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

//UUID ...
func (o *Options) UUID() string {
	return o.uuid
}

//Name ...
func (o *Options) Name() string {
	return o.name
}

//Version ...
func (o *Options) Version() string {
	return o.version
}

//Metadata ...
func (o *Options) Metadata() map[string]string {
	return o.metadata
}
