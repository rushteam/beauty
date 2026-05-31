package consul

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/conf"
)

func init() {
	conf.RegisterFactory("consul", newConfigCenterFromURL)
}

// newConfigCenterFromURL 从 URL 构造 consul ConfigCenter。
// 格式：consul://[token@]host:port/kv/path?datacenter=dc1&namespace=ns
// token 通过 URL 的 password 字段传入：consul://:mytoken@host:8500/...
func newConfigCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	c := &Config{
		Addr: u.Host,
	}
	if u.User != nil {
		c.Token, _ = u.User.Password()
	}
	q := u.Query()
	c.Datacenter = q.Get("datacenter")
	c.Namespace = q.Get("namespace")
	c.Partition = q.Get("partition")
	return NewConfigCenter(c)
}
