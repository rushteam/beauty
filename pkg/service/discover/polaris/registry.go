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
	"github.com/rushteam/beauty/pkg/service/discover/internal/clientpool"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"maps"
	"strings"
)

var _ discover.RegistryDiscovery = (*Registry)(nil)

type consumerClient struct {
	api     api.ConsumerAPI
	destroy func()
}

var (
	newConsumerClient = func(c *Config) (*consumerClient, error) {
		sdkContext, err := newSDKContext(c)
		if err != nil {
			return nil, err
		}
		return &consumerClient{api: api.NewConsumerAPIByContext(sdkContext), destroy: sdkContext.Destroy}, nil
	}

	// consumers 按连接配置共享 Find/Watch 使用的 SDK 上下文，
	// 最后一个使用方释放并闲置超时后 Destroy。
	consumers = clientpool.New(clientpool.DefaultIdleTimeout, func(c *consumerClient) {
		c.destroy()
	})
)

func newSDKContext(c *Config) (api.SDKContext, error) {
	configuration := config.NewDefaultConfiguration(c.Addresses)
	if c.Token != "" {
		configuration.Global.ServerConnector.Token = c.Token
	}
	sdkContext, err := api.InitContextByConfig(configuration)
	if err != nil {
		return nil, fmt.Errorf("polaris init context failed: %w", err)
	}
	return sdkContext, nil
}

// NewRegistry 创建 Registry。发现侧 SDK 上下文在 Find/Watch 期间从共享池借用，
// Registry 本身不持有它；注册侧上下文仅在调用 Register 时创建。
func NewRegistry(c *Config) *Registry {
	return &Registry{c: c, codec: c.effectiveCodec()}
}

type Registry struct {
	c           *Config
	codec       discover.Codec
	initOnce    sync.Once
	initErr     error
	providerAPI api.ProviderAPI
}

func (r *Registry) initClient() error {
	r.initOnce.Do(func() {
		sdkContext, err := newSDKContext(r.c)
		if err != nil {
			r.initErr = err
			return
		}
		r.providerAPI = api.NewProviderAPIByContext(sdkContext)
	})
	return r.initErr
}

// acquireConsumer 借用共享的发现侧 ConsumerAPI，用完必须调用 release。
func (r *Registry) acquireConsumer() (api.ConsumerAPI, func(), error) {
	key := strings.Join(r.c.Addresses, ",") + "\x00" + r.c.Token
	c, release, err := consumers.Acquire(key, func() (*consumerClient, error) {
		return newConsumerClient(r.c)
	})
	if err != nil {
		return nil, release, err
	}
	return c.api, release, nil
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
	consumer, release, err := r.acquireConsumer()
	if err != nil {
		return nil, err
	}
	defer release()

	req := &api.GetInstancesRequest{}
	req.Service = serviceName
	req.Namespace = r.c.Namespace

	resp, err := consumer.GetInstances(req)
	if err != nil {
		return nil, fmt.Errorf("polaris GetInstances failed for service %s: %w", serviceName, err)
	}
	return buildServiceInfos(resp.Instances, r.effectiveCodec()), nil
}

func (r *Registry) effectiveCodec() discover.Codec {
	if r.codec != nil {
		return r.codec
	}
	return r.c.effectiveCodec()
}

// Watch 使用 WatchAllInstances (v1.7.0 推荐 API) 监听实例变化，带重连退避。
// Watch 是阻塞的，直到 ctx 取消才返回，与 etcd/nacos 保持一致。
func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	consumer, release, err := r.acquireConsumer()
	if err != nil {
		return err
	}
	defer release()

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

		watchResp, err := consumer.WatchAllInstances(&api.WatchAllInstancesRequest{
			WatchAllInstancesRequest: model.WatchAllInstancesRequest{
				ServiceKey: model.ServiceKey{
					Namespace: r.c.Namespace,
					Service:   serviceName,
				},
				WatchMode:         model.WatchModeNotify,
				InstancesListener: &instancesListener{update: update, serviceName: serviceName, codec: r.effectiveCodec()},
			},
		})
		if err != nil {
			logger.Error("polaris WatchAllInstances failed",
				slog.String("service", serviceName), slog.Any("err", err))
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 8*time.Second)
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
	codec       discover.Codec
}

func (l *instancesListener) OnInstancesUpdate(resp *model.InstancesResponse) {
	if resp == nil {
		return
	}
	services := buildServiceInfos(resp.GetInstances(), l.codec)
	logger.Info("polaris instances updated",
		slog.String("service", l.serviceName),
		slog.Int("count", len(services)),
	)
	l.update(services)
}

// UpdateCallResult 上报一次 gRPC 调用结果，触发 Polaris 熔断/统计逻辑。
// instance 通过 Find 获得；delay 为本次调用耗时；retCode 为 gRPC 状态码（0=OK）。
func (r *Registry) UpdateCallResult(instance model.Instance, retStatus model.RetStatus, delay time.Duration, retCode int32) {
	consumer, release, err := r.acquireConsumer()
	if err != nil {
		logger.Warn("polaris UpdateServiceCallResult skipped", slog.Any("err", err))
		return
	}
	defer release()
	result := &api.ServiceCallResult{}
	result.SetCalledInstance(instance)
	result.SetRetStatus(retStatus)
	result.SetDelay(delay)
	result.SetRetCode(retCode)
	if err := consumer.UpdateServiceCallResult(result); err != nil {
		logger.Warn("polaris UpdateServiceCallResult failed", slog.Any("err", err))
	}
}

func buildServiceInfos(instances []model.Instance, codec discover.Codec) []discover.ServiceInfo {
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
		info := discover.ServiceInfo{
			ID:       instance.GetId(),
			Name:     instance.GetService(),
			Addr:     net.JoinHostPort(instance.GetHost(), fmt.Sprintf("%d", instance.GetPort())),
			Metadata: metadata,
		}
		if !codec.Accept(info) {
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
