package nacos

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"sort"
	"strconv"
	"sync"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/rushteam/beauty/pkg/infra/nacos"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
)

var _ discover.RegistryDiscovery = (*Registry)(nil)

// var instance = make(map[string]*Registry)
// var mu sync.Mutex

func NewRegistry(c *Config) *Registry {
	return &Registry{
		c:       c,
		codec:   c.effectiveCodec(),
		clients: make(map[string]naming_client.INamingClient),
	}
}

type Registry struct {
	c       *Config
	codec   discover.Codec
	mu      sync.Mutex
	clients map[string]naming_client.INamingClient
}

func (r *Registry) client(key string) (naming_client.INamingClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[key]; ok {
		return c, nil
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
		return nil, fmt.Errorf("nacos naming client error: %w", err)
	}
	r.clients[key] = client
	return client, nil
}

func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	host, port := addr.ParseHostAndPort(info.Addr())
	portUint, err := strconv.ParseUint(port, 10, 64)
	if err != nil {
		return func() {}, fmt.Errorf("nacos register: invalid port %q for service %s: %w", port, info.Name(), err)
	}
	registerClient, err := r.client(info.ID())
	if err != nil {
		return func() {}, err
	}

	// 构建注册元数据：copy 一份避免修改调用方原始 map，并自动注入网关兼容字段。
	// "protocol" 字段使 Higress 等云原生网关能自动识别后端协议（HTTP/GRPC/HTTPS/GRPCS），
	// 无需额外配置 higress.io/backend-protocol 注解。
	// 用户可通过 WithServiceMetadata 显式设置 "protocol" 来覆盖自动推断。
	meta := make(map[string]string)
	maps.Copy(meta, info.Metadata())
	if _, ok := meta["kind"]; !ok {
		meta["kind"] = info.Kind()
	}
	if _, ok := meta["protocol"]; !ok {
		meta["protocol"] = kindToProtocol(info.Kind())
	}

	if _, err := registerClient.RegisterInstance(vo.RegisterInstanceParam{
		Ip:          host,
		Port:        portUint,
		Weight:      r.c.Weight,
		Enable:      true,
		Healthy:     true,
		Metadata:    meta,
		ServiceName: info.Name(),
		ClusterName: r.c.Cluster,
		GroupName:   r.c.Group,
		Ephemeral:   true,
	}); err != nil {
		return func() {}, fmt.Errorf("nacos RegisterInstance failed for service %s: %w", info.Name(), err)
	}
	logger.Info("nacos RegisterInstance success",
		slog.String("svc.id", info.ID()),
		slog.String("svc.name", info.Name()),
		slog.String("svc.addr", info.Addr()),
		slog.Any("svc.meta", meta),
	)
	return func() {
		_, err := registerClient.DeregisterInstance(vo.DeregisterInstanceParam{
			Ip:          host,
			Port:        portUint,
			ServiceName: info.Name(),
			Cluster:     r.c.Cluster,
			GroupName:   r.c.Group,
			Ephemeral:   true,
		})
		if err != nil {
			logger.Error("nacos DeregisterInstance failed",
				slog.Any("err", err),
				slog.String("svc.id", info.ID()),
				slog.String("svc.name", info.Name()),
				slog.String("svc.addr", info.Addr()),
				slog.Any("svc.meta", info.Metadata()),
			)
			return
		}
		logger.Info("nacos DeregisterInstance success",
			slog.String("svc.id", info.ID()),
			slog.String("svc.name", info.Name()),
			slog.String("svc.addr", info.Addr()),
			slog.Any("svc.meta", info.Metadata()),
		)
	}, nil
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	findClient, err := r.client("find")
	if err != nil {
		return nil, err
	}
	instances, err := findClient.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		Clusters:    []string{r.c.Cluster},
		GroupName:   r.c.Group,
		HealthyOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("nacos SelectInstances failed for service %s: %w", serviceName, err)
	}
	return r.filterInstances(instances), nil
}

func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	registryClient, err := r.client("watch")
	if err != nil {
		return err
	}
	// 拉取初始列表；失败只记日志，不阻止 Watch 继续
	if services, err := registryClient.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		Clusters:    []string{r.c.Cluster},
		GroupName:   r.c.Group,
		HealthyOnly: true,
	}); err != nil {
		logger.Warn("nacos SelectInstances failed", slog.String("service", serviceName), slog.Any("err", err))
	} else if len(services) > 0 {
		update(r.filterInstances(services))
	}

	// 订阅回调，用于 Unsubscribe 时匹配
	subscribeCallback := func(services []model.Instance, err error) {
		if err != nil {
			logger.Warn("nacos service update error", slog.Any("err", err))
			return
		}
		logger.Info("nacos service update", slog.Any("services", services))
		update(r.filterInstances(services))
	}

	go func() {
		<-ctx.Done()
		if err := registryClient.Unsubscribe(&vo.SubscribeParam{
			ServiceName:       serviceName,
			Clusters:          []string{r.c.Cluster},
			GroupName:         r.c.Group,
			SubscribeCallback: subscribeCallback,
		}); err != nil {
			logger.Warn("nacos Unsubscribe failed", slog.String("service", serviceName), slog.Any("err", err))
		}
	}()
	return registryClient.Subscribe(&vo.SubscribeParam{
		ServiceName:       serviceName,
		Clusters:          []string{r.c.Cluster},
		GroupName:         r.c.Group,
		SubscribeCallback: subscribeCallback,
	})
}

func (r *Registry) filterInstances(services []model.Instance) []discover.ServiceInfo {
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
		if v.Metadata == nil {
			v.Metadata = make(map[string]string)
		}
		info := discover.ServiceInfo{
			ID:       v.InstanceId,
			Name:     v.ServiceName,
			Addr:     net.JoinHostPort(v.Ip, fmt.Sprintf("%d", v.Port)),
			Metadata: v.Metadata,
		}
		if !r.codec.Accept(info) {
			logger.Warn("service not accepted by codec", slog.Any("v", v))
			continue
		}
		ss = append(ss, info)
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].Name == ss[j].Name {
			return ss[i].ID < ss[j].ID
		}
		return ss[i].Name < ss[j].Name
	})
	return ss
}

// kindToProtocol 将 beauty 的 kind 映射为云原生网关识别的后端协议字符串。
// Higress 通过 Nacos 实例 metadata["protocol"] 判断转发协议
// (参见 higress registry/nacos/v2/watcher.go generateServiceEntry)。
func kindToProtocol(kind string) string {
	switch kind {
	case "grpc":
		return "GRPC"
	case "grpcs":
		return "GRPCS"
	case "https":
		return "HTTPS"
	default:
		return "HTTP"
	}
}
