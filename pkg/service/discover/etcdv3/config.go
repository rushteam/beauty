package etcdv3

import (
	"net/url"
	"strings"
)

type Config struct {
	Endpoints []string `mapstructure:"endpoints"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	Prefix    string   `mapstructure:"prefix"`
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
