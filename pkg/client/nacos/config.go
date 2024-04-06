package nacos

import (
	"fmt"
	"net/url"
	"strings"
)

type Config struct {
	Addr      []string `mapstructure:"addr"`
	Namespace string   `mapstructure:"namespace" schema:"namespace"`
	Weight    float64  `mapstructure:"weight" schema:"weight"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	AppName   string   `mapstructure:"app_name" schema:"app_name"`
}

func (c *Config) String() string {
	return buildUniq(c)
}

func buildUniq(c *Config) string {
	var user *url.Userinfo
	if len(c.Username) > 0 {
		user = url.User(c.Username)
		if len(c.Password) > 0 {
			user = url.UserPassword(c.Username, c.Password)
		}
	}
	u := url.URL{
		Scheme:   "nacos",
		User:     user,
		Host:     strings.Join(c.Addr, ","),
		Path:     c.Namespace,
		RawQuery: fmt.Sprintf("app_name=%s&weight=%v", c.AppName, c.Weight),
	}
	return u.String()
}
