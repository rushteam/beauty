package mojito

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// var _ registry.Service = (*Options)(nil)

//OptionsFunc ..
type OptionsFunc func(o *Options)

//Name ..
func Name(name string) OptionsFunc {
	return func(o *Options) {
		o.Name = name
	}
}

//Version ..
func Version(verison string) OptionsFunc {
	return func(o *Options) {
		o.Vers = verison
	}
}

// Options ..
type Options struct {
	Kind string            `json:"kind"`
	Name string            `json:"name"`
	UUID string            `json:"uuid"`
	Vers string            `json:"version"`
	Meta map[string]string `json:"meta"`
}

//NewOptions new a options
func NewOptions(kind string, opts ...OptionsFunc) *Options {
	o := &Options{
		Kind: kind,
		Name: kind,
		UUID: uuid.New().String(),
		Meta: make(map[string]string),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

//String ..
func (o *Options) String() string {
	return fmt.Sprintf("%v-%v", o.Kind, o.Name)
}

//ID ..
func (o *Options) ID() string {
	return o.UUID
}

//Version ...
func (o *Options) Version() string {
	return o.Vers
}

//Metadata ...
func (o *Options) Metadata() map[string]string {
	return o.Meta
}

//Encode return string for registry value
func (o *Options) Encode() string {
	v, _ := json.Marshal(o)
	return string(v)
}
