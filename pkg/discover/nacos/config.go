package nacos

import (
	"fmt"
	"net/url"
	"strings"
)

type Config struct {
	Addr      []string `json:"addr"`
	Cluster   string   `json:"cluster" schema:"cluster"`
	Namespace string   `json:"namespace" schema:"namespace"`
	Group     string   `json:"group" schema:"group"`
	Weight    float64  `json:"weight" schema:"weight"`
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	AppName   string   `schema:"app_name"`
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
		Scheme:   "nacos",
		User:     user,
		Host:     strings.Join(c.Addr, ","),
		Path:     c.Namespace,
		RawQuery: fmt.Sprintf("app_name=%s&weight=%v", c.AppName, c.Weight),
	}
	return u.String()
}
