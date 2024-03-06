package conf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
)

func init() {
	RegistryLoader("nacos", &FileLoader{})
}

type NacosLoader struct {
	client config_client.IConfigClient
	DataID string
	Group  string
}

func (l *NacosLoader) Load(key string, dst interface{}) error {
	content, err := l.client.GetConfig(vo.ConfigParam{
		DataId:  l.DataID,
		Group:   l.Group,
		Content: "",
		DatumId: "",
		Type:    "",
		OnChange: func(namespace string, group string, dataId string, data string) {

		},
	})
	if err != nil {
		return fmt.Errorf("failed to get nacos config: %v", err)
	}
	if err := json.Unmarshal([]byte(content), dst); err != nil {
		return fmt.Errorf("failed to unmarshal nacos config: %v", err)
	}
	return nil
}

func (l *NacosLoader) LoadAndWatch(ctx context.Context, key string, dst interface{}) error {
	content, err := l.client.GetConfig(vo.ConfigParam{
		DataId:  l.DataID,
		Group:   l.Group,
		Content: "",
		DatumId: "",
		Type:    "",
		OnChange: func(namespace string, group string, dataId string, data string) {
			fmt.Println(namespace, group, dataId, data)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to get nacos config: %v", err)
	}
	if err := json.Unmarshal([]byte(content), dst); err != nil {
		return fmt.Errorf("failed to unmarshal nacos config: %v", err)
	}
	return nil
}

func NewNacosLoader(client config_client.IConfigClient, dataID, group string) (Loader, error) {
	return &NacosLoader{
		client: client,
		DataID: dataID,
		Group:  group,
	}, nil
}

func NewNacosConfigClient(servers []string, cc *constant.ClientConfig) (config_client.IConfigClient, error) {
	var serverConfigs []constant.ServerConfig
	for _, v := range servers {
		sc, err := parseURLToServerConfig(v)
		if err != nil {
			return nil, fmt.Errorf("failed to nacos loader with server config")
		}
		serverConfigs = append(serverConfigs, sc)
	}
	return clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig:  cc,
		ServerConfigs: serverConfigs,
	})
}

func parseURLToServerConfig(urlStr string) (constant.ServerConfig, error) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return constant.ServerConfig{}, err
	}
	var port uint64 = 8848 // 默认端口号
	if parsed.Port() != "" {
		if parsedPort, err := strconv.ParseUint(parsed.Port(), 1, 64); err == nil {
			port = parsedPort
		}
	}
	return constant.ServerConfig{
		IpAddr:      parsed.Hostname(),
		Port:        port,
		ContextPath: parsed.Path,
		Scheme:      parsed.Scheme,
	}, nil
}
