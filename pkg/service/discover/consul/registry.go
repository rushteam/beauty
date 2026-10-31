package consul

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	beautyconsul "github.com/rushteam/beauty/pkg/infra/consul"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
)

var _ discover.RegistryDiscovery = (*Registry)(nil)

type Registry struct {
	c      *Config
	codec  discover.Codec
	client *api.Client
}

func NewRegistry(c *Config) *Registry {
	// 复用 pkg/infra/consul 的连接构造,与配置中心/分布式锁共用同一处参数覆盖。
	client, err := beautyconsul.NewClient(&beautyconsul.Config{
		Addr:       c.Addr,
		Token:      c.Token,
		Namespace:  c.Namespace,
		Partition:  c.Partition,
		Datacenter: c.Datacenter,
	})
	if err != nil {
		logger.Error("consul: failed to create client", slog.Any("err", err))
		return nil
	}
	return &Registry{c: c, codec: c.effectiveCodec(), client: client}
}

func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	host, portStr := addr.ParseHostAndPort(info.Addr())
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return func() {}, fmt.Errorf("consul register: invalid port %q for service %s: %w", portStr, info.Name(), err)
	}

	// 构建注册元数据：自动注入 "protocol" 使 Higress 等云原生网关能正确识别后端协议，
	// 无需额外配置 higress.io/backend-protocol 注解。
	meta := make(map[string]string)
	maps.Copy(meta, info.Metadata())
	if _, ok := meta["kind"]; !ok {
		meta["kind"] = info.Kind()
	}
	if _, ok := meta["protocol"]; !ok {
		meta["protocol"] = kindToProtocol(info.Kind())
	}

	reg := &api.AgentServiceRegistration{
		ID:      info.ID(),
		Name:    info.Name(),
		Address: host,
		Port:    port,
		Meta:    meta,
		Check: &api.AgentServiceCheck{
			TTL:                            "30s",
			DeregisterCriticalServiceAfter: "60s",
		},
	}

	if err := r.register(ctx, reg); err != nil {
		return func() {}, fmt.Errorf("consul register: %w", err)
	}

	logger.Info("consul register success",
		slog.String("svc.id", info.ID()),
		slog.String("svc.name", info.Name()),
		slog.String("svc.addr", info.Addr()),
	)

	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		r.heartbeat(hbCtx, reg)
	}()

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			// 先等心跳 goroutine 退出，避免它在注销之后又把实例重新注册回去
			stopHeartbeat()
			<-hbDone
			dctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.client.Agent().ServiceDeregisterOpts(info.ID(), (&api.QueryOptions{}).WithContext(dctx)); err != nil {
				logger.Error("consul deregister failed",
					slog.Any("err", err),
					slog.String("svc.id", info.ID()),
					slog.String("svc.name", info.Name()),
				)
				return
			}
			logger.Info("consul deregister success",
				slog.String("svc.id", info.ID()),
				slog.String("svc.name", info.Name()),
			)
		})
	}, nil
}

// ttlInterval 心跳间隔，须明显小于检查的 TTL（30s）。
var ttlInterval = 10 * time.Second

// register 注册服务并立即上报一次 passing：TTL 检查初始为 critical，
// 不上报的话实例要等到第一次心跳才对调用方可见。
func (r *Registry) register(ctx context.Context, reg *api.AgentServiceRegistration) error {
	if err := r.client.Agent().ServiceRegisterOpts(reg, (&api.ServiceRegisterOpts{}).WithContext(ctx)); err != nil {
		return err
	}
	return r.client.Agent().UpdateTTLOpts("service:"+reg.ID, "", api.HealthPassing, (&api.QueryOptions{}).WithContext(ctx))
}

// heartbeat 定期上报 TTL。上报失败时重新注册：agent 重启丢失本地状态，或心跳中断超过
// DeregisterCriticalServiceAfter 后实例已被摘除，此时只续 TTL 永远不会恢复。
// Consul 的注册是幂等的，agent 不可达时重注册同样失败，下个周期再试。
func (r *Registry) heartbeat(ctx context.Context, reg *api.AgentServiceRegistration) {
	ticker := time.NewTicker(ttlInterval)
	defer ticker.Stop()
	checkID := "service:" + reg.ID
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := r.client.Agent().UpdateTTLOpts(checkID, "", api.HealthPassing, (&api.QueryOptions{}).WithContext(ctx))
		if err == nil || ctx.Err() != nil {
			continue
		}
		logger.Warn("consul ttl pass failed, re-registering",
			slog.String("svc.id", reg.ID), slog.Any("err", err))
		if err := r.register(ctx, reg); err != nil {
			if ctx.Err() == nil {
				logger.Error("consul re-register failed",
					slog.String("svc.id", reg.ID), slog.Any("err", err))
			}
			continue
		}
		logger.Info("consul re-register success", slog.String("svc.id", reg.ID))
	}
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	entries, _, err := r.client.Health().ServiceMultipleTags(
		serviceName, nil, true,
		(&api.QueryOptions{}).WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("consul find %s: %w", serviceName, err)
	}
	return r.filterEntries(entries), nil
}

var (
	watchRetryBase = time.Second
	watchRetryMax  = 8 * time.Second
)

// Watch 基于阻塞查询监听实例变化，阻塞直到 ctx 取消。
// 首次查询 WaitIndex=0 立即返回，即初始列表；之后按内容比较，有变化才通知。
func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	var (
		lastIndex uint64
		last      []discover.ServiceInfo
		notified  bool
		backoff   = watchRetryBase
	)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, meta, err := r.client.Health().ServiceMultipleTags(
			serviceName, nil, true,
			(&api.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  30 * time.Second,
			}).WithContext(ctx),
		)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Warn("consul watch error",
				slog.String("service", serviceName), slog.Any("err", err))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, watchRetryMax)
			continue
		}
		backoff = watchRetryBase
		lastIndex = nextWaitIndex(lastIndex, meta.LastIndex)

		services := r.filterEntries(entries)
		if !notified || !slices.EqualFunc(last, services, discover.ServiceInfo.Equal) {
			update(services)
			last, notified = services, true
		}
	}
}

// nextWaitIndex 按 Consul 阻塞查询规范计算下一次的 WaitIndex：
// index 回退（集群重建、快照恢复、切换到落后的 server）时重置为 0 重新拉全量，
// 否则后续变化的 index 都小于旧值，会被永远忽略；index < 1 时用 1，避免阻塞查询立即返回形成忙循环。
func nextWaitIndex(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	if cur < 1 {
		return 1
	}
	return cur
}

func (r *Registry) filterEntries(entries []*api.ServiceEntry) []discover.ServiceInfo {
	var ss []discover.ServiceInfo
	for _, e := range entries {
		if e.Checks.AggregatedStatus() != api.HealthPassing {
			continue
		}
		meta := e.Service.Meta
		if meta == nil {
			meta = make(map[string]string)
		}
		svcAddr := net.JoinHostPort(e.Service.Address, strconv.Itoa(e.Service.Port))
		if e.Service.Address == "" {
			svcAddr = net.JoinHostPort(e.Node.Address, strconv.Itoa(e.Service.Port))
		}
		info := discover.ServiceInfo{
			ID:       e.Service.ID,
			Name:     e.Service.Service,
			Addr:     svcAddr,
			Metadata: meta,
		}
		if !r.codec.Accept(info) {
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
// Higress 通过 Consul service meta["protocol"] 判断转发协议。
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
