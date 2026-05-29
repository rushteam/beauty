package k8s

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func init() {
	// 注册 k8s 工厂
	discover.RegisterFactoryFunc("k8s", createRegistryFromURL)
	discover.RegisterFactoryFunc("kubernetes", createRegistryFromURL) // 别名
}

// createRegistryFromURL 从URL创建k8s注册中心
func createRegistryFromURL(targetURL *url.URL) (discover.RegistryDiscovery, error) {
	config, err := NewFromURL(*targetURL)
	if err != nil {
		return nil, err
	}
	return NewRegistry(config), nil
}
