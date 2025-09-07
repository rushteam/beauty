package etcdv3

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func init() {
	// 注册 etcd 工厂
	discover.RegisterFactoryFunc("etcd", createRegistryFromURL)
	discover.RegisterFactoryFunc("etcdv3", createRegistryFromURL) // 别名
}

// createRegistryFromURL 从URL创建etcd注册中心
func createRegistryFromURL(targetURL *url.URL) (discover.Discovery, error) {
	registry, err := NewFromURL(*targetURL)
	if err != nil {
		return nil, err
	}
	return registry, nil
}
