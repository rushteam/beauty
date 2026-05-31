package etcd

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/rushteam/beauty/pkg/conf"
)

func init() {
	conf.RegisterFactory("etcd", newConfigCenterFromURL)
	conf.RegisterFactory("etcdv3", newConfigCenterFromURL)
}

// newConfigCenterFromURL 从 URL 构造 etcd ConfigCenter。
// 格式：etcd://[user:pass@]host1,host2/key?dial_ms=3000
func newConfigCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	c := &Config{
		Endpoints: strings.Split(u.Host, ","),
		DialMS:    3000,
	}
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	if ms := u.Query().Get("dial_ms"); ms != "" {
		if v, err := strconv.Atoi(ms); err == nil {
			c.DialMS = v
		}
	}
	return NewConfigCenter(c)
}
