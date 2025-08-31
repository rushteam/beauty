package polaris

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
)

var (
	_ discover.Registry  = (*Registry)(nil)
	_ discover.Discovery = (*Registry)(nil)
)

func NewRegistry(c *Config) *Registry {
	return &Registry{
		c:  c,
		mu: &sync.Mutex{},
	}
}

type Registry struct {
	c           *Config
	mu          *sync.Mutex
	providerAPI api.ProviderAPI
	consumerAPI api.ConsumerAPI
	initialized bool
}

func (r *Registry) initClient() error {
	if r.initialized {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		return nil
	}

	// 创建配置
	configuration := config.NewDefaultConfiguration(r.c.Addresses)

	// 设置全局配置
	if r.c.Namespace != "" {
		configuration.Global.ServerConnector.Addresses = r.c.Addresses
	}

	// 创建 SDK 上下文
	sdkContext, err := api.InitContextByConfig(configuration)
	if err != nil {
		return fmt.Errorf("polaris init context failed: %w", err)
	}

	// 创建 ProviderAPI
	r.providerAPI = api.NewProviderAPIByContext(sdkContext)

	// 创建 ConsumerAPI
	r.consumerAPI = api.NewConsumerAPIByContext(sdkContext)

	r.initialized = true
	return nil
}

func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	if err := r.initClient(); err != nil {
		return func() {}, err
	}

	host, portStr := addr.ParseHostAndPort(info.Addr())
	port, _ := strconv.Atoi(portStr)

	// 构建实例请求
	request := &api.InstanceRegisterRequest{}
	request.Service = info.Name()
	request.Namespace = r.c.Namespace
	request.Host = host
	request.Port = port
	request.Protocol = &r.c.Protocol
	request.Version = &r.c.Version
	request.Weight = &r.c.Weight
	request.Priority = &r.c.Priority
	request.TTL = &r.c.TTL

	// 设置元数据
	if info.Metadata() != nil {
		metadata := make(map[string]string)
		for k, v := range info.Metadata() {
			metadata[k] = v
		}
		// 确保 kind 字段存在
		if _, ok := metadata["kind"]; !ok {
			metadata["kind"] = info.Kind()
		}
		request.Metadata = metadata
	} else {
		request.Metadata = map[string]string{
			"kind": info.Kind(),
		}
	}

	// 注册实例
	resp, err := r.providerAPI.RegisterInstance(request)
	if err != nil {
		logger.Error("polaris RegisterInstance failed",
			slog.Any("err", err),
			slog.String("svc.id", info.ID()),
			slog.String("svc.name", info.Name()),
			slog.String("svc.addr", info.Addr()),
			slog.Any("svc.meta", info.Metadata()),
		)
		return func() {}, err
	}

	logger.Info("polaris RegisterInstance success",
		slog.String("svc.id", info.ID()),
		slog.String("svc.name", info.Name()),
		slog.String("svc.addr", info.Addr()),
		slog.Any("svc.meta", info.Metadata()),
		slog.String("instance.id", resp.InstanceID),
	)

	// 启动心跳
	heartbeatRequest := &api.InstanceHeartbeatRequest{}
	heartbeatRequest.Service = info.Name()
	heartbeatRequest.Namespace = r.c.Namespace
	heartbeatRequest.Host = host
	heartbeatRequest.Port = port
	heartbeatRequest.InstanceID = resp.InstanceID

	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	go r.startHeartbeat(heartbeatCtx, heartbeatRequest)

	return func() {
		// 停止心跳
		heartbeatCancel()

		// 注销实例
		deregisterRequest := &api.InstanceDeRegisterRequest{}
		deregisterRequest.Service = info.Name()
		deregisterRequest.Namespace = r.c.Namespace
		deregisterRequest.Host = host
		deregisterRequest.Port = port
		deregisterRequest.InstanceID = resp.InstanceID

		if err := r.providerAPI.Deregister(deregisterRequest); err != nil {
			logger.Error("polaris DeregisterInstance failed",
				slog.Any("err", err),
				slog.String("svc.id", info.ID()),
				slog.String("svc.name", info.Name()),
				slog.String("svc.addr", info.Addr()),
				slog.Any("svc.meta", info.Metadata()),
			)
			return
		}

		logger.Info("polaris DeregisterInstance success",
			slog.String("svc.id", info.ID()),
			slog.String("svc.name", info.Name()),
			slog.String("svc.addr", info.Addr()),
			slog.Any("svc.meta", info.Metadata()),
		)
	}, nil
}

func (r *Registry) startHeartbeat(ctx context.Context, request *api.InstanceHeartbeatRequest) {
	ticker := time.NewTicker(time.Duration(r.c.TTL/3) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.providerAPI.Heartbeat(request); err != nil {
				logger.Warn("polaris heartbeat failed", slog.Any("err", err))
			}
		}
	}
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	if err := r.initClient(); err != nil {
		return nil, err
	}

	request := &api.GetInstancesRequest{}
	request.Service = serviceName
	request.Namespace = r.c.Namespace

	resp, err := r.consumerAPI.GetInstances(request)
	if err != nil {
		return nil, fmt.Errorf("polaris get instances failed: %w", err)
	}

	return buildServiceInfos(resp.Instances), nil
}

func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	if err := r.initClient(); err != nil {
		return err
	}

	// 首先获取一次实例列表
	instances, err := r.Find(ctx, serviceName)
	if err == nil && len(instances) > 0 {
		update(instances)
	}

	// 使用 Polaris 的事件监听机制
	go func() {
		// 创建服务监听请求
		watchRequest := &api.WatchServiceRequest{}
		watchRequest.Key = model.ServiceKey{
			Namespace: r.c.Namespace,
			Service:   serviceName,
		}

		// 开始监听服务变化
		watchResp, err := r.consumerAPI.WatchService(watchRequest)
		if err != nil {
			logger.Error("polaris watch service failed",
				slog.Any("err", err),
				slog.String("service", serviceName))
			return
		}

		// 监听事件通道
		for {
			select {
			case <-ctx.Done():
				logger.Info("polaris watch service context cancelled",
					slog.String("service", serviceName))
				return
			case event, ok := <-watchResp.EventChannel:
				if !ok {
					logger.Warn("polaris watch service event channel closed",
						slog.String("service", serviceName))
					return
				}

				// 处理实例事件
				if _, ok := event.(*model.InstanceEvent); ok {
					logger.Info("polaris service instance event received",
						slog.String("service", serviceName),
						slog.Int("event_type", int(event.GetSubScribeEventType())))

					// 重新获取最新的实例列表
					currentInstances, err := r.Find(ctx, serviceName)
					if err != nil {
						logger.Warn("polaris get instances after event failed",
							slog.Any("err", err),
							slog.String("service", serviceName))
						continue
					}

					logger.Info("polaris service instances updated",
						slog.String("service", serviceName),
						slog.Int("instances", len(currentInstances)))

					// 更新服务实例列表
					update(currentInstances)
				} else {
					logger.Debug("polaris received non-instance event",
						slog.String("service", serviceName),
						slog.Int("event_type", int(event.GetSubScribeEventType())))
				}
			}
		}
	}()

	return nil
}

func buildServiceInfos(instances []model.Instance) []discover.ServiceInfo {
	var ss []discover.ServiceInfo

	for _, instance := range instances {
		if !instance.IsHealthy() {
			logger.Warn("polaris service not healthy", slog.Any("instance", instance))
			continue
		}

		if instance.GetWeight() <= 0 {
			logger.Warn("polaris service weight<=0", slog.Any("instance", instance))
			continue
		}

		metadata := instance.GetMetadata()
		if metadata == nil {
			metadata = make(map[string]string)
		}

		// 检查 kind 字段
		if metadata["kind"] != "grpc" {
			logger.Warn("polaris service metadata.kind != grpc", slog.Any("instance", instance))
			continue
		}

		ss = append(ss, discover.ServiceInfo{
			ID:       instance.GetId(),
			Name:     instance.GetService(),
			Addr:     net.JoinHostPort(instance.GetHost(), fmt.Sprintf("%d", instance.GetPort())),
			Metadata: metadata,
		})
	}

	// 稳定排序（与 etcd 和 nacos 保持一致）
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].Name == ss[j].Name {
			return ss[i].ID < ss[j].ID
		}
		return ss[i].Name < ss[j].Name
	})

	return ss
}
