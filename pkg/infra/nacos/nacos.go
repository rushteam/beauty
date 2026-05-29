package nacos

import (
	"fmt"
	"net"
	"strconv"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

func NewNamingClient(c *Config) (naming_client.INamingClient, error) {
	var serverConfigs []constant.ServerConfig
	for _, address := range c.Addr {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("nacos: invalid address %q: %w", address, err)
		}
		portUint, err := strconv.ParseUint(port, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("nacos: invalid port in address %q: %w", address, err)
		}
		serverConfigs = append(serverConfigs,
			*constant.NewServerConfig(host, portUint,
				constant.WithScheme("http"),
			),
		)
	}
	var clientOpts = []constant.ClientOption{
		constant.WithNotLoadCacheAtStart(true),
		constant.WithTimeoutMs(5000),
		constant.WithUpdateCacheWhenEmpty(true),
		// constant.WithLogDir("/tmp/nacos/log"),
		// constant.WithCacheDir("/tmp/nacos/cache"),
		// constant.WithLogLevel("info"),
	}
	if len(c.AppName) > 0 {
		clientOpts = append(clientOpts, constant.WithAppName(c.AppName))
	}
	if len(c.Namespace) > 0 {
		clientOpts = append(clientOpts, constant.WithNamespaceId(c.Namespace))
	}
	if len(c.Username) > 0 {
		clientOpts = append(clientOpts, constant.WithUsername(c.Username))
	}
	if len(c.Password) > 0 {
		clientOpts = append(clientOpts, constant.WithPassword(c.Password))
	}
	client, err := clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig:  constant.NewClientConfig(clientOpts...),
		ServerConfigs: serverConfigs,
	})
	if err != nil {
		return nil, fmt.Errorf("nacos naming client: %v", err)
	}
	return client, nil
}
