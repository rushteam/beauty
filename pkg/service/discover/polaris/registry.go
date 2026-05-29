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
	"maps"
)

var (
	_ discover.Registry  = (*Registry)(nil)
	_ discover.Discovery = (*Registry)(nil)
)

func NewRegistry(c *Config) *Registry {
	return &Registry{c: c}
}

type Registry struct {
	c           *Config
	initOnce    sync.Once
	initErr     error
	providerAPI api.ProviderAPI
	consumerAPI api.ConsumerAPI
}

func (r *Registry) initClient() error {
	r.initOnce.Do(func() {
		configuration := config.NewDefaultConfiguration(r.c.Addresses)

		if r.c.Token != "" {
			configuration.Global.ServerConnector.Token = r.c.Token
		}

		sdkContext, err := api.InitContextByConfig(configuration)
		if err != nil {
			r.initErr = fmt.Errorf("polaris init context failed: %w", err)
			return
		}
		r.providerAPI = api.NewProviderAPIByContext(sdkContext)
		r.consumerAPI = api.NewConsumerAPIByContext(sdkContext)
	})
	return r.initErr
}

func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	if err := r.initClient(); err != nil {
		return func() {}, err
	}

	host, portStr := addr.ParseHostAndPort(info.Addr())
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return func() {}, fmt.Errorf("polaris register: invalid port %q for service %s: %w", portStr, info.Name(), err)
	}

	ttl := r.c.TTL
	if ttl <= 0 {
		ttl = 5
	}
	healthy := true
	weight := r.c.Weight
	if weight <= 0 {
		weight = 100
	}
	priority := r.c.Priority
	version := r.c.Version
	if version == "" {
		version = "1.0.0"
	}
	protocol := r.c.Protocol
	if protocol == "" {
		protocol = "grpc"
	}

	// 合并 metadata，确保 kind 字段
	metadata := make(map[string]string)
	maps.Copy(metadata, info.Metadata())
	if _, ok := metadata["kind"]; !ok {
		metadata["kind"] = info.Kind()
	}

	request := &api.InstanceRegisterRequest{}
	request.Service = info.Name()
	request.Namespace = r.c.Namespace
	request.Host = host
	request.Port = port
	request.Protocol = &protocol
	request.Version = &version
	request.Weight = &weight
	request.Priority = &priority
	request.Healthy = &healthy
	request.Metadata = metadata
	// AutoHeartbeat = true：SDK 自动维持心跳，无需手动 Heartbeat 轮询
	request.AutoHeartbeat = true
	request.SetTTL(ttl)

	resp, err := r.providerAPI.RegisterInstance(request)
	if err != nil {
		return func() {}, fmt.Errorf("polaris RegisterInstance failed for service %s: %w", info.Name(), err)
	}

	logger.Info("polaris RegisterInstance success",
		slog.String("svc.id", info.ID()),
		slog.String("svc.name", info.Name()),
		slog.String("svc.addr", info.Addr()),
		slog.String("instance.id", resp.InstanceID),
	)

	return func() {
		deregReq := &api.InstanceDeRegisterRequest{}
		deregReq.Service = info.Name()
		deregReq.Namespace = r.c.Namespace
		deregReq.Host = host
		deregReq.Port = port
		deregReq.InstanceID = resp.InstanceID

		if err := r.providerAPI.Deregister(deregReq); err != nil {
			logger.Error("polaris DeregisterInstance failed",
				slog.Any("err", err),
				slog.String("svc.id", info.ID()),
				slog.String("svc.name", info.Name()),
				slog.String("svc.addr", info.Addr()),
			)
			return
		}
		logger.Info("polaris DeregisterInstance success",
			slog.String("svc.id", info.ID()),
			slog.String("svc.name", info.Name()),
			slog.String("svc.addr", info.Addr()),
		)
	}, nil
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	if err := r.initClient(); err != nil {
		return nil, err
	}

	req := &api.GetInstancesRequest{}
	req.Service = serviceName
	req.Namespace = r.c.Namespace

	resp, err := r.consumerAPI.GetInstances(req)
	if err != nil {
		return nil, fmt.Errorf("polaris GetInstances failed for service %s: %w", serviceName, err)
	}
	return buildServiceInfos(resp.Instances), nil
}

// Watch 使用 WatchAllInstances (v1.7.0 推荐 API) 监听实例变化，带重连退避。
// Watch 是阻塞的，直到 ctx 取消才返回，与 etcd/nacos 保持一致。
func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	if err := r.initClient(); err != nil {
		return err
	}

	// 先推送一次初始快照，失败只记日志不阻塞
	if instances, err := r.Find(ctx, serviceName); err != nil {
		logger.Warn("polaris Watch: initial Find failed", slog.String("service", serviceName), slog.Any("err", err))
	} else {
		update(instances)
	}

	backoff := 200 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return nil
		}

		watchResp, err := r.consumerAPI.WatchAllInstances(&api.WatchAllInstancesRequest{
			WatchAllInstancesRequest: model.WatchAllInstancesRequest{
				ServiceKey: model.ServiceKey{
					Namespace: r.c.Namespace,
					Service:   serviceName,
				},
				WatchMode:         model.WatchModeNotify,
				InstancesListener: &instancesListener{update: update, serviceName: serviceName},
			},
		})
		if err != nil {
			logger.Error("polaris WatchAllInstances failed",
				slog.String("service", serviceName), slog.Any("err", err))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
				if backoff < 8*time.Second {
					backoff *= 2
				}
			}
			continue
		}

		backoff = 200 * time.Millisecond
		// 阻塞直到 ctx 取消
		<-ctx.Done()
		watchResp.CancelWatch()
		return nil
	}
}

// instancesListener 实现 model.InstancesListener，由 Polaris SDK 在实例变更时回调。
type instancesListener struct {
	update      discover.Notify
	serviceName string
}

func (l *instancesListener) OnInstancesUpdate(resp *model.InstancesResponse) {
	if resp == nil {
		return
	}
	services := buildServiceInfos(resp.GetInstances())
	logger.Info("polaris instances updated",
		slog.String("service", l.serviceName),
		slog.Int("count", len(services)),
	)
	l.update(services)
}

// UpdateCallResult 上报一次 gRPC 调用结果，触发 Polaris 熔断/统计逻辑。
// instance 通过 Find 获得；delay 为本次调用耗时；retCode 为 gRPC 状态码（0=OK）。
func (r *Registry) UpdateCallResult(instance model.Instance, retStatus model.RetStatus, delay time.Duration, retCode int32) {
	if r.consumerAPI == nil {
		return
	}
	result := &api.ServiceCallResult{}
	result.SetCalledInstance(instance)
	result.SetRetStatus(retStatus)
	result.SetDelay(delay)
	result.SetRetCode(retCode)
	if err := r.consumerAPI.UpdateServiceCallResult(result); err != nil {
		logger.Warn("polaris UpdateServiceCallResult failed", slog.Any("err", err))
	}
}

func buildServiceInfos(instances []model.Instance) []discover.ServiceInfo {
	var ss []discover.ServiceInfo
	for _, instance := range instances {
		if !instance.IsHealthy() {
			continue
		}
		if instance.IsIsolated() {
			continue
		}
		if instance.GetWeight() <= 0 {
			continue
		}

		metadata := instance.GetMetadata()
		if metadata == nil {
			metadata = make(map[string]string)
		}
		if metadata["kind"] != "grpc" {
			continue
		}

		ss = append(ss, discover.ServiceInfo{
			ID:       instance.GetId(),
			Name:     instance.GetService(),
			Addr:     net.JoinHostPort(instance.GetHost(), fmt.Sprintf("%d", instance.GetPort())),
			Metadata: metadata,
		})
	}

	sort.Slice(ss, func(i, j int) bool {
		if ss[i].Name == ss[j].Name {
			return ss[i].ID < ss[j].ID
		}
		return ss[i].Name < ss[j].Name
	})
	return ss
}
