package nacos

import (
	"net/url"
	"strings"

	"github.com/gorilla/schema"
)

type Config struct {
	Addr      []string `mapstructure:"addr"`
	Cluster   string   `mapstructure:"cluster" schema:"cluster"`
	Group     string   `mapstructure:"group" schema:"group"`
	Namespace string   `mapstructure:"namespace" schema:"namespace"`
	Weight    float64  `mapstructure:"weight" schema:"weight"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	AppName   string   `mapstructure:"app_name" schema:"app_name"`
}

// func (c *Config) String() string {
// 	var user *url.Userinfo
// 	if len(c.Username) > 0 {
// 		user = url.User(c.Username)
// 		if len(c.Password) > 0 {
// 			user = url.UserPassword(c.Username, c.Password)
// 		}
// 	}
// 	u := url.URL{
// 		Scheme:   "nacos",
// 		User:     user,
// 		Host:     strings.Join(c.Addr, ","),
// 		Path:     c.Namespace,
// 		RawQuery: fmt.Sprintf("app_name=%s&weight=%v", c.AppName, c.Weight),
// 	}
// 	return u.String()
// }

func NewFromURL(u url.URL) (*Registry, error) {
	c := &Config{}
	c.Addr = strings.Split(u.Host, ",")
	c.Weight = 100
	c.AppName = "beauty"
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
