package nacos

import (
	"net/url"
	"strings"

	"github.com/rushteam/beauty/pkg/conf"
)

func init() {
	conf.RegisterFactory("nacos", newConfigCenterFromURL)
}

// newConfigCenterFromURL 从 URL 构造 nacos ConfigCenter。
// 格式：nacos://[user:pass@]host:port/dataID?namespace=dev&group=DEFAULT_GROUP
// 多个地址用逗号分隔 host：nacos://h1:8848,h2:8848/dataID
func newConfigCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	c := &Config{
		Addr: strings.Split(u.Host, ","),
	}
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	q := u.Query()
	c.Namespace = q.Get("namespace")
	c.AppName = q.Get("app_name")
	group := q.Get("group")
	return NewConfigCenter(c, group)
}
