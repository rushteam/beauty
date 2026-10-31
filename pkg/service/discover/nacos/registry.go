package nacos

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/rushteam/beauty/pkg/infra/nacos"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/discover/internal/clientpool"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
)

var _ discover.RegistryDiscovery = (*Registry)(nil)

var (
	newNamingClient = nacos.NewNamingClient

	// discoveryClients 按连接配置共享 Find/Watch 使用的 naming client，
	// 最后一个使用方释放并闲置超时后 CloseClient。
	discoveryClients = clientpool.New(clientpool.DefaultIdleTimeout, func(c naming_client.INamingClient) {
		c.CloseClient()
	})
)

// NewRegistry 创建 Registry。Registry 本身不持有连接：发现侧客户端在 Find/Watch
// 期间从共享池借用，注册侧客户端由 Register 创建、在注销时关闭，因此无需 Close。
func NewRegistry(c *Config) *Registry {
	return &Registry{
		c:     c,
		codec: c.effectiveCodec(),
	}
}

type Registry struct {
	c     *Config
	codec discover.Codec
}

func (r *Registry) newClient() (naming_client.INamingClient, error) {
	client, err := newNamingClient(&nacos.Config{
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
	return client, nil
}

// acquireDiscoveryClient 借用共享的发现侧 naming client，用完必须调用 release。
func (r *Registry) acquireDiscoveryClient() (naming_client.INamingClient, func(), error) {
	key := strings.Join([]string{
		strings.Join(r.c.Addr, ","), r.c.Namespace, r.c.Username, r.c.Password, r.c.AppName,
	}, "\x00")
	return discoveryClients.Acquire(key, r.newClient)
}

// Register 为每个实例使用独立的 naming client，返回的注销函数会在注销后关闭该 client。
func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	host, port := addr.ParseHostAndPort(info.Addr())
	portUint, err := strconv.ParseUint(port, 10, 64)
	if err != nil {
		return func() {}, fmt.Errorf("nacos register: invalid port %q for service %s: %w", port, info.Name(), err)
	}
	registerClient, err := r.newClient()
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
		registerClient.CloseClient()
		return func() {}, fmt.Errorf("nacos RegisterInstance failed for service %s: %w", info.Name(), err)
	}
	logger.Info("nacos RegisterInstance success",
		slog.String("svc.id", info.ID()),
		slog.String("svc.name", info.Name()),
		slog.String("svc.addr", info.Addr()),
		slog.Any("svc.meta", meta),
	)
	return func() {
		defer registerClient.CloseClient()
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
	findClient, release, err := r.acquireDiscoveryClient()
	if err != nil {
		return nil, err
	}
	defer release()
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
	registryClient, release, err := r.acquireDiscoveryClient()
	if err != nil {
		return err
	}
	defer release()
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
		filtered := r.filterInstances(services)
		logger.Info("nacos service update",
			slog.String("service", serviceName),
			slog.Int("total", len(services)),
			slog.Int("available", len(filtered)))
		update(filtered)
	}

	// Nacos 是事件/回调模型：Subscribe 注册回调后立即返回。
	// 但上层 resolver 把 Watch 当成长阻塞调用（见 pkg/client/grpcclient/resolver.go
	// Resolver.Start），只在 ctx 取消或后端断连时才应返回；若 Watch 订阅完立即返回
	// nil，resolver 会误判为“异常断连”并不断重订阅，每次都多挂一个 Nacos 回调和一个
	// goroutine，直到连接关闭才释放。因此这里订阅成功后必须阻塞到 ctx 取消再
	// Unsubscribe，返回 ctx.Err() 让 resolver 识别为正常退出。
	// Subscribe/Unsubscribe 使用同一个 param（同一回调闭包），保证取消订阅精确匹配。
	param := &vo.SubscribeParam{
		ServiceName:       serviceName,
		Clusters:          []string{r.c.Cluster},
		GroupName:         r.c.Group,
		SubscribeCallback: subscribeCallback,
	}
	if err := registryClient.Subscribe(param); err != nil {
		return err
	}
	<-ctx.Done()
	if err := registryClient.Unsubscribe(param); err != nil {
		logger.Warn("nacos Unsubscribe failed", slog.String("service", serviceName), slog.Any("err", err))
	}
	return ctx.Err()
}

func (r *Registry) filterInstances(services []model.Instance) []discover.ServiceInfo {
	var ss []discover.ServiceInfo
	for _, v := range services {
		if !v.Healthy {
			logger.Debug("nacos instance skipped: not healthy", slog.String("id", v.InstanceId))
			continue
		}
		if !v.Enable {
			logger.Debug("nacos instance skipped: not enabled", slog.String("id", v.InstanceId))
			continue
		}
		if v.Weight <= 0 {
			logger.Debug("nacos instance skipped: weight<=0", slog.String("id", v.InstanceId))
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
			logger.Debug("nacos instance skipped: not accepted by codec", slog.String("id", v.InstanceId))
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
