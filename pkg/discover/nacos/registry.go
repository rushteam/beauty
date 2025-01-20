package nacos

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

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

var instance = make(map[string]*Registry)
var mu sync.Mutex

func BuildRegistryWithURL(u url.URL) *Registry {
	c := &Config{
		Addr:      strings.Split(u.Host, ","),
		Cluster:   "", //from url schema
		Namespace: "", //from url schema
		Group:     "", //from url schema
		Weight:    100,
		AppName:   "beauty",
	}
	if u.User != nil {
		c.Username = u.User.Username()
		c.Password, _ = u.User.Password()
	}
	decoder := schema.NewDecoder()
	decoder.Decode(c, u.Query())
	key := c.String()
	mu.Lock()
	defer mu.Unlock()
	if client, ok := instance[key]; ok {
		return client
	}
	instance[key] = NewRegistry(c)
	return instance[key]
}

func NewRegistry(c *Config) *Registry {
	return &Registry{
		c:       c,
		mu:      &sync.Mutex{},
		clients: make(map[string]naming_client.INamingClient),
	}
}

type Registry struct {
	c       *Config
	mu      *sync.Mutex
	clients map[string]naming_client.INamingClient
}

func (r Registry) client(key string) naming_client.INamingClient {
	if c, ok := r.clients[key]; ok {
		return c
	}
	client, err := nacos.NewNamingClient(&nacos.Config{
		Addr:      r.c.Addr,
		Namespace: r.c.Namespace,
		Weight:    r.c.Weight,
		Username:  r.c.Username,
		Password:  r.c.Password,
		AppName:   r.c.AppName,
	})
	if err != nil {
		logger.Error("nacos naming client error", slog.Any("err", err))
		return nil
	}
	r.mu.Lock()
	r.clients[key] = client
	r.mu.Unlock()
	return client
}
func (r Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	addr, port := addr.ParseHostAndPort(info.Addr())
	portUint, _ := strconv.ParseUint(port, 10, 64)
	_, err := r.client(info.ID()).RegisterInstance(vo.RegisterInstanceParam{
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
		logger.Error("nacos RegisterInstance failed", slog.Any("err", err), slog.String("svc.id", info.ID()), slog.String("svc.name", info.Name()), slog.String("svc.addr", info.Addr()), slog.Any("svc.meta", info.Metadata()))
		return func() {}, nil
	}
	logger.Info("nacos RegisterInstance success", slog.String("svc.id", info.ID()), slog.String("svc.name", info.Name()), slog.String("svc.addr", info.Addr()), slog.Any("svc.meta", info.Metadata()))
	return func() {
		_, err := r.client(info.ID()).DeregisterInstance(vo.DeregisterInstanceParam{
			Ip:          addr,
			Port:        portUint,
			ServiceName: info.Name(),
			Cluster:     r.c.Cluster,
			GroupName:   r.c.Group,
			Ephemeral:   true,
		})
		if err != nil {
			logger.Error("nacos DeregisterInstance failed", slog.Any("err", err), slog.String("svc.id", info.ID()), slog.String("svc.name", info.Name()), slog.String("svc.addr", info.Addr()), slog.Any("svc.meta", info.Metadata()))
			return
		}
		logger.Info("nacos DeregisterInstance success", slog.String("svc.id", info.ID()), slog.String("svc.name ", info.Name()), slog.String("svc.addr", info.Addr()), slog.Any("svc.meta", info.Metadata()))
	}, nil
}

func (r Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	return []discover.ServiceInfo{}, nil
}

func (r Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	go func() {
		<-ctx.Done()
		r.client("watch").Unsubscribe(&vo.SubscribeParam{
			ServiceName:       serviceName,
			Clusters:          []string{r.c.Cluster},
			GroupName:         r.c.Group,
			SubscribeCallback: func(services []model.Instance, err error) {},
		})
	}()

	return r.client("watch").Subscribe(&vo.SubscribeParam{
		ServiceName: serviceName,
		Clusters:    []string{r.c.Cluster},
		GroupName:   r.c.Group,
		SubscribeCallback: func(services []model.Instance, err error) {
			if err != nil {
				logger.Warn("nacos service update error", slog.Any("err", err))
				return
			}
			logger.Info("nacos service update", slog.Any("services", services))
			update(buildService(services))
		},
	})
}

func buildService(services []model.Instance) []discover.ServiceInfo {
	var ss []discover.ServiceInfo
	for _, v := range services {
		if !v.Healthy {
			logger.Warn("service not healthy", slog.Any("v", v))
			continue
		}
		if !v.Enable {
			logger.Warn("service not enable", slog.Any("v", v))
			continue
		}
		if v.Weight <= 0 {
			logger.Warn("service weight<=0", slog.Any("v", v))
			continue
		}
		if v.Metadata["kind"] != "grpc" {
			logger.Warn("service metadata.kind != grpc", slog.Any("v", v))
			continue
		}
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
