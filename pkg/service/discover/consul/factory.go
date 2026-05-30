package consul

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func init() {
	discover.RegisterFactoryFunc("consul", createRegistryFromURL)
}

func createRegistryFromURL(targetURL *url.URL) (discover.RegistryDiscovery, error) {
	return NewFromURL(*targetURL)
}
