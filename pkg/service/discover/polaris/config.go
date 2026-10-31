package polaris

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/schema"
	"github.com/rushteam/beauty/pkg/service/discover"
)

type Config struct {
	Addresses []string `mapstructure:"addresses"`
	Namespace string   `mapstructure:"namespace" schema:"namespace"`
	Token     string   `mapstructure:"token" schema:"token"`
	Service   string   `mapstructure:"service" schema:"service"`
	TTL       int      `mapstructure:"ttl" schema:"ttl"`
	Weight    int      `mapstructure:"weight" schema:"weight"`
	Priority  int      `mapstructure:"priority" schema:"priority"`
	Version   string   `mapstructure:"version" schema:"version"`
	Protocol  string   `mapstructure:"protocol" schema:"protocol"`

	// Codec 自定义过滤策略，用于兼容其他框架的服务实例或做 HTTP 服务发现。
	// 为 nil 时使用 beauty 原生格式（仅接受 kind="grpc"）。
	Codec discover.Codec `mapstructure:"-" schema:"-"`

	// CodecName 通过名称引用已注册的 Codec（discover.RegisterCodec）。
	// URL 方式使用：polaris://host/ns?codec=accept_all
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
	c.Addresses = strings.Split(u.Host, ",")
	c.Namespace = strings.TrimPrefix(u.Path, "/")
	if c.Namespace == "" {
		c.Namespace = "default"
	}

	// 设置默认值
	c.TTL = 30
	c.Weight = 100
	c.Priority = 0
	c.Version = "1.0.0"
	c.Protocol = "grpc"

	if u.User != nil {
		c.Token, _ = u.User.Password()
	}

	decoder := schema.NewDecoder()
	if err := decoder.Decode(c, u.Query()); err != nil {
		return nil, err
	}

	return NewRegistry(c), nil
}

func (c *Config) String() string {
	var user *url.Userinfo
	if len(c.Token) > 0 {
		user = url.UserPassword("", c.Token)
	}

	query := url.Values{}
	if c.TTL != 30 {
		query.Set("ttl", strconv.Itoa(c.TTL))
	}
	if c.Weight != 100 {
		query.Set("weight", strconv.Itoa(c.Weight))
	}
	if c.Priority != 0 {
		query.Set("priority", strconv.Itoa(c.Priority))
	}
	if c.Version != "1.0.0" {
		query.Set("version", c.Version)
	}
	if c.Protocol != "grpc" {
		query.Set("protocol", c.Protocol)
	}
	if c.CodecName != "" {
		query.Set("codec", c.CodecName)
	}

	u := url.URL{
		Scheme:   "polaris",
		User:     user,
		Host:     strings.Join(c.Addresses, ","),
		Path:     "/" + c.Namespace,
		RawQuery: query.Encode(),
	}
	return u.String()
}
