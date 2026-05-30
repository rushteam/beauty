package nacos

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

func NewConfigClient(c *Config) (config_client.IConfigClient, error) {
	var serverConfigs []constant.ServerConfig
	for _, address := range c.Addr {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("nacos config: invalid address %q: %w", address, err)
		}
		portUint, err := strconv.ParseUint(port, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("nacos config: invalid port in address %q: %w", address, err)
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
	client, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig:  constant.NewClientConfig(clientOpts...),
		ServerConfigs: serverConfigs,
	})
	if err != nil {
		return nil, fmt.Errorf("nacos config: %w", err)
	}
	return client, nil
}

type ConfigCenter struct {
	client config_client.IConfigClient
	group  string
}

func NewConfigCenter(c *Config, group string) (*ConfigCenter, error) {
	if group == "" {
		group = "DEFAULT_GROUP"
	}
	client, err := NewConfigClient(c)
	if err != nil {
		return nil, err
	}
	return &ConfigCenter{client: client, group: group}, nil
}

func (cc *ConfigCenter) Get(_ context.Context, dataID string) (string, error) {
	content, err := cc.client.GetConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  cc.group,
	})
	if err != nil {
		return "", fmt.Errorf("nacos config: %w", err)
	}
	return content, nil
}

func (cc *ConfigCenter) Watch(ctx context.Context, dataID string, onChange func(key, value string)) (context.CancelFunc, error) {
	err := cc.client.ListenConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  cc.group,
		OnChange: func(_, _, dataId, data string) {
			onChange(dataId, data)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("nacos config: %w", err)
	}
	cancel := func() {
		_ = cc.client.CancelListenConfig(vo.ConfigParam{
			DataId: dataID,
			Group:  cc.group,
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return cancel, nil
}
