package nacos

import (
	"net/url"
	"strings"
)

type Config struct {
	Addr      []string `json:"addr"`
	Cluster   string   `json:"cluster"`
	Namespace string   `json:"namespace"`
	Group     string   `json:"group"`
	Weight    float64  `json:"weight"`
}

func (c *Config) String() string {
	u := url.URL{
		Scheme: "nacos",
		// User:   user,
		Host: strings.Join(c.Addr, ","),
		Path: c.Namespace,
	}
	return u.String()
}
