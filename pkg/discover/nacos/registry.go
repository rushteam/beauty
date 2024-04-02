package nacos

import (
	"context"
	"log/slog"
	"net"
	"strconv"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
)

var (
	_ discover.Registry  = (*Registry)(nil)
	_ discover.Discovery = (*Registry)(nil)
)

var instance = make(map[string]*Registry)

func NewRegistry(c *Config) *Registry {
	key := c.String()
	if v, ok := instance[key]; ok {
		return v
	}
	var serverConfigs []constant.ServerConfig
	for _, v := range c.Addr {
		host, port := addr.ParseHostAndPort(v)
		portUint, _ := strconv.ParseUint(port, 10, 64)
		serverConfigs = append(
			serverConfigs,
			*constant.NewServerConfig(host, portUint,
				constant.WithScheme("http"),
			),
		)
	}
	client, err := clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig: constant.NewClientConfig(
			constant.WithNotLoadCacheAtStart(true),
			constant.WithTimeoutMs(5000),
			constant.WithNamespaceId(c.Namespace), //When namespace is public, fill in the blank string here.
			// constant.WithLogDir("/tmp/nacos/log"),
			// constant.WithCacheDir("/tmp/nacos/cache"),
			// constant.WithLogLevel("info"),
		),
		ServerConfigs: serverConfigs,
	})
	if err != nil {
		logger.Error("nacos Registry client error", slog.Any("err", err))
		return nil
	}
	r := &Registry{
		c:      c,
		client: client,
	}
	instance[c.String()] = r
	return r
}

type Registry struct {
	c      *Config
	client naming_client.INamingClient
	discover.Registry
}

func (r Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	addr, port := addr.ParseHostAndPort(info.Addr())
	portUint, _ := strconv.ParseUint(port, 10, 64)
	_, err := r.client.RegisterInstance(vo.RegisterInstanceParam{
		Ip:          addr,
		Port:        portUint,
		Weight:      r.c.Weight,
		Enable:      true,
		Healthy:     true,
		Metadata:    info.Metadata(),
		ServiceName: info.Name(),
		ClusterName: r.c.Cluster,
		GroupName:   r.c.Group,
		Ephemeral:   true,
	})
	if err != nil {
		logger.Error("nacos DeregisterInstance failed", slog.Any("err", err), slog.String("svc.name", info.Name()))
		return func() {}, nil
	}
	return func() {
		_, err := r.client.DeregisterInstance(vo.DeregisterInstanceParam{
			Ip:          addr,
			Port:        portUint,
			ServiceName: info.Name(),
			Cluster:     r.c.Cluster,
			GroupName:   r.c.Group,
			Ephemeral:   true,
		})
		if err != nil {
			logger.Error("nacos DeregisterInstance failed", slog.Any("err", err), slog.String("svc.name", info.Name()))
		}
	}, nil
}

func (r Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	return []discover.ServiceInfo{}, nil
}

func (r Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	go func() {
		<-ctx.Done()
		r.client.Unsubscribe(&vo.SubscribeParam{
			ServiceName:       serviceName,
			Clusters:          []string{r.c.Cluster},
			GroupName:         r.c.Group,
			SubscribeCallback: func(services []model.Instance, err error) {},
		})
	}()
	return r.client.Subscribe(&vo.SubscribeParam{
		ServiceName: serviceName,
		Clusters:    []string{r.c.Cluster},
		GroupName:   r.c.Group,
		SubscribeCallback: func(services []model.Instance, err error) {
			if err != nil {
				return
			}
			update(buildService(services))
		},
	})
}

func buildService(services []model.Instance) []discover.ServiceInfo {
	var ss []discover.ServiceInfo
	for _, v := range services {
		port := strconv.FormatUint(v.Port, 10)
		ss = append(ss, discover.ServiceInfo{
			ID:       v.InstanceId,
			Name:     v.ServiceName,
			Addr:     net.JoinHostPort(v.Ip, port),
			Metadata: v.Metadata,
		})
	}
	return ss
}
