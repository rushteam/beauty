package etcdv3

import (
	"net/url"
	"strings"

	"github.com/gorilla/schema"
)

type Config struct {
	Endpoints []string `mapstructure:"endpoints"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	Prefix    string   `mapstructure:"prefix"`
	TTL       int64    `mapstructure:"ttl" schema:"ttl"`
	DialMS    int      `mapstructure:"dial_ms" schema:"dial_ms"`
}

func (c *Config) String() string {
	var user *url.Userinfo
	if len(c.Username) > 0 {
		user = url.User(c.Username)
		if len(c.Password) > 0 {
			user = url.UserPassword(c.Username, c.Password)
		}
	}
	u := url.URL{
		Scheme: "etcd",
		User:   user,
		Host:   strings.Join(c.Endpoints, ","),
		Path:   c.Prefix,
	}
	return u.String()
}

func NewFromURL(u url.URL) (*Registry, error) {
	c := &Config{}
	c.Endpoints = strings.Split(u.Host, ",")
	c.Prefix = strings.TrimPrefix(u.Path, "/")
	if c.Prefix == "" {
		c.Prefix = "beauty"
	}
	// defaults
	c.TTL = 10
	c.DialMS = 3000
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	decoder := schema.NewDecoder()
	if err := decoder.Decode(c, u.Query()); err != nil {
		return nil, err
	}
	return NewRegistry(c), nil
}
