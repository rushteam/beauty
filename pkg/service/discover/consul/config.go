package consul

import (
	"net/url"

	"github.com/gorilla/schema"
	"github.com/rushteam/beauty/pkg/service/discover"
)

type Config struct {
	Addr       string `mapstructure:"addr"`
	Token      string `mapstructure:"token" schema:"token"`
	Namespace  string `mapstructure:"namespace" schema:"namespace"`
	Partition  string `mapstructure:"partition" schema:"partition"`
	Datacenter string `mapstructure:"datacenter" schema:"datacenter"`

	// Codec 自定义过滤策略，用于兼容其他框架的服务实例。
	// 为 nil 时使用 beauty 原生格式（仅接受 kind="grpc"）。
	Codec discover.Codec `mapstructure:"-" schema:"-"`

	// CodecName 通过名称引用已注册的 Codec（discover.RegisterCodec）。
	// URL 方式使用：consul://host/svc?codec=accept_all
	// 优先级：Codec > CodecName > 默认 beauty。
	CodecName string `mapstructure:"codec_name" schema:"codec"`
}

func (c *Config) effectiveCodec() discover.Codec {
	if c != nil && c.Codec != nil {
		return c.Codec
	}
	if c != nil && c.CodecName != "" {
		if codec, ok := discover.GetCodec(c.CodecName); ok {
			return codec
		}
	}
	return discover.NewBeautyCodec()
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
