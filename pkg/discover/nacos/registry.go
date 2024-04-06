package nacos

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/schema"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/client/nacos"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
)

var (
	_ discover.Registry  = (*Registry)(nil)
	_ discover.Discovery = (*Registry)(nil)
)

func NewRegistryWithURL(u url.URL) *Registry {
	c := &Config{
		Addr:      strings.Split(u.Host, ","),
		Cluster:   "",
		Namespace: "",
		Group:     "",
		Weight:    100,
		AppName:   "beauty",
	}
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	decoder := schema.NewDecoder()
	decoder.Decode(c, u.Query())
	return NewRegistry(c)
}

func NewRegistry(c *Config) *Registry {
	return &Registry{
		c: c,
		client: nacos.NewNamingClient(&nacos.Config{
			Addr:      c.Addr,
			Cluster:   c.Cluster,
			Namespace: c.Namespace,
			Group:     c.Group,
			Weight:    c.Weight,
			Username:  c.Username,
			Password:  c.Password,
			AppName:   c.AppName,
		}),
	}
}

type Registry struct {
	c      *Config
	client naming_client.INamingClient
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
		logger.Error("nacos RegisterInstance failed", slog.Any("err", err), slog.String("svc.name", info.Name()))
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
