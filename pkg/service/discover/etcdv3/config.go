package etcdv3

import (
	"net/url"
	"strings"

	"github.com/gorilla/schema"
	"github.com/rushteam/beauty/pkg/service/discover"
)

type Config struct {
	Endpoints []string `mapstructure:"endpoints"`
	Username  string   `mapstructure:"username"`
	Password  string   `mapstructure:"password"`
	Prefix    string   `mapstructure:"prefix"`
	TTL       int64    `mapstructure:"ttl" schema:"ttl"`
	DialMS    int      `mapstructure:"dial_ms" schema:"dial_ms"`

	// Codec 自定义 KV 编解码策略，用于兼容其他框架的注册格式。
	// 为 nil 时使用 beauty 原生格式。
	// 三方格式实现见 contrib/codec/gozero、contrib/codec/kratos 等。
	Codec discover.KVCodec `mapstructure:"-" schema:"-"`

	// CodecName 通过名称引用已注册的 KVCodec（discover.RegisterKVCodec）。
	// URL 方式使用：etcd://host/svc?codec=kratos
	// 优先级：Codec > CodecName > 默认 beauty。
	CodecName string `mapstructure:"codec_name" schema:"codec"`
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

func (c *Config) effectiveKVCodec() discover.KVCodec {
	if c != nil && c.Codec != nil {
		return c.Codec
	}
	if c != nil && c.CodecName != "" {
		if codec, ok := discover.GetKVCodec(c.CodecName); ok {
			return codec
		}
	}
	prefix := "beauty"
	if c != nil && c.Prefix != "" {
		prefix = c.Prefix
	}
	return discover.NewBeautyKVCodec(prefix)
}

func NewFromURL(u url.URL) (*Registry, error) {
	c := &Config{}
	c.Endpoints = strings.Split(u.Host, ",")
	c.Prefix = strings.TrimPrefix(u.Path, "/")
	if c.Prefix == "" {
		c.Prefix = "beauty"
	}
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
