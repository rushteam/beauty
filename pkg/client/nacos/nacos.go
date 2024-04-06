package nacos

import (
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

var instance = make(map[string]naming_client.INamingClient)
var mu sync.Mutex

func NewNamingClient(c *Config) (naming_client.INamingClient, error) {
	key := c.String()
	if v, ok := instance[key]; ok {
		return v, nil
	}
	var serverConfigs []constant.ServerConfig
	for _, addr := range c.Addr {
		host, port, _ := net.SplitHostPort(addr)
		portUint, _ := strconv.ParseUint(port, 10, 64)
		serverConfigs = append(serverConfigs,
			*constant.NewServerConfig(host, portUint,
				constant.WithScheme("http"),
			),
		)
	}
	var clientOpts = []constant.ClientOption{
		constant.WithNotLoadCacheAtStart(true),
		constant.WithTimeoutMs(5000),
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
	mu.Lock()
	defer mu.Unlock()
	instance[c.String()] = client
	return client, nil
}
