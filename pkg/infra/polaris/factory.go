package polaris

import (
	"net/url"
	"strings"

	"github.com/rushteam/beauty/pkg/conf"
)

func init() {
	conf.RegisterFactory("polaris", newConfigCenterFromURL)
}

// newConfigCenterFromURL 从 URL 构造 polaris ConfigCenter。
// 格式：polaris://host1:8091,host2:8091/fileGroup/fileName?namespace=default
func newConfigCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	addrs := strings.Split(u.Host, ",")
	// polaris 地址需要带 scheme，统一补 grpc://
	for i, a := range addrs {
		if !strings.Contains(a, "://") {
			addrs[i] = "grpc://" + a
		}
	}
	c := &Config{
		Addresses: addrs,
		Namespace: u.Query().Get("namespace"),
	}
	if u.User != nil {
		c.Token, _ = u.User.Password()
	}
	return NewConfigCenter(c)
}
