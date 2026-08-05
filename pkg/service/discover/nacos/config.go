package nacos

import (
	"net/url"
	"strings"

	"github.com/gorilla/schema"
	"github.com/rushteam/beauty/pkg/service/discover"
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

	// Codec 自定义过滤策略，用于兼容其他框架的服务实例。
	// 为 nil 时使用 beauty 原生格式（仅接受 kind="grpc"）。
	Codec discover.Codec `mapstructure:"-" schema:"-"`
}

func (c *Config) effectiveCodec() discover.Codec {
	if c != nil && c.Codec != nil {
		return c.Codec
	}
	return discover.NewBeautyCodec()
}

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
