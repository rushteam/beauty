package polaris

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func init() {
	// 注册 polaris 工厂
	discover.RegisterFactoryFunc("polaris", createRegistryFromURL)
}

// createRegistryFromURL 从URL创建polaris注册中心
func createRegistryFromURL(targetURL *url.URL) (discover.RegistryDiscovery, error) {
	return NewFromURL(*targetURL)
}
