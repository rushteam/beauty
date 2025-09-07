package nacos

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func init() {
	// 注册 nacos 工厂
	discover.RegisterFactoryFunc("nacos", createRegistryFromURL)
}

// createRegistryFromURL 从URL创建nacos注册中心
func createRegistryFromURL(targetURL *url.URL) (discover.Discovery, error) {
	registry, err := NewFromURL(*targetURL)
	if err != nil {
		return nil, err
	}
	return registry, nil
}
