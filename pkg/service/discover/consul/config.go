package consul

import (
	"net/url"

	"github.com/gorilla/schema"
)

type Config struct {
	Addr      string `mapstructure:"addr"`
	Token     string `mapstructure:"token" schema:"token"`
	Namespace string `mapstructure:"namespace" schema:"namespace"`
	Partition string `mapstructure:"partition" schema:"partition"`
	Datacenter string `mapstructure:"datacenter" schema:"datacenter"`
}

func NewFromURL(u url.URL) (*Registry, error) {
	c := &Config{}
	c.Addr = u.Host
	if u.User != nil {
		c.Token, _ = u.User.Password()
	}
	decoder := schema.NewDecoder()
	if err := decoder.Decode(c, u.Query()); err != nil {
		return nil, err
	}
	return NewRegistry(c), nil
}
