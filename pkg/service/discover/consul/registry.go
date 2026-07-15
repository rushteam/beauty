package consul

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"sort"
	"strconv"
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
	return &Registry{c: c, client: client}
}

func (r *Registry) Register(ctx context.Context, info discover.Service) (context.CancelFunc, error) {
	host, portStr := addr.ParseHostAndPort(info.Addr())
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return func() {}, fmt.Errorf("consul register: invalid port %q for service %s: %w", portStr, info.Name(), err)
	}

	meta := make(map[string]string)
	maps.Copy(meta, info.Metadata())
	if _, ok := meta["kind"]; !ok {
		meta["kind"] = info.Kind()
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

	if err := r.client.Agent().ServiceRegisterOpts(reg, (&api.ServiceRegisterOpts{}).WithContext(ctx)); err != nil {
		return func() {}, fmt.Errorf("consul register: %w", err)
	}

	logger.Info("consul register success",
		slog.String("svc.id", info.ID()),
		slog.String("svc.name", info.Name()),
		slog.String("svc.addr", info.Addr()),
	)

	// 定期更新 TTL 心跳
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		checkID := "service:" + info.ID()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if err := r.client.Agent().PassTTL(checkID, ""); err != nil {
					logger.Warn("consul ttl pass failed",
						slog.String("svc.id", info.ID()),
						slog.Any("err", err),
					)
				}
			}
		}
	}()

	return func() {
		close(stopCh)
		if err := r.client.Agent().ServiceDeregister(info.ID()); err != nil {
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
	}, nil
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	entries, _, err := r.client.Health().ServiceMultipleTags(
		serviceName, nil, true,
		(&api.QueryOptions{}).WithContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("consul find %s: %w", serviceName, err)
	}
	return buildServiceInfos(entries), nil
}

func (r *Registry) Watch(ctx context.Context, serviceName string, update discover.Notify) error {
	// 先推送初始列表
	if services, err := r.Find(ctx, serviceName); err != nil {
		logger.Warn("consul watch: initial find failed",
			slog.String("service", serviceName), slog.Any("err", err))
	} else {
		update(services)
	}

	go func() {
		var lastIndex uint64
		for {
			if ctx.Err() != nil {
				return
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
					return
				}
				logger.Warn("consul watch error",
					slog.String("service", serviceName), slog.Any("err", err))
				time.Sleep(time.Second)
				continue
			}
			if meta.LastIndex > lastIndex {
				lastIndex = meta.LastIndex
				update(buildServiceInfos(entries))
			}
		}
	}()

	return nil
}

func buildServiceInfos(entries []*api.ServiceEntry) []discover.ServiceInfo {
	var ss []discover.ServiceInfo
	for _, e := range entries {
		if e.Checks.AggregatedStatus() != api.HealthPassing {
			continue
		}
		meta := e.Service.Meta
		if meta == nil {
			meta = make(map[string]string)
		}
		if meta["kind"] != "grpc" {
			continue
		}
		addr := net.JoinHostPort(e.Service.Address, strconv.Itoa(e.Service.Port))
		if e.Service.Address == "" {
			addr = net.JoinHostPort(e.Node.Address, strconv.Itoa(e.Service.Port))
		}
		ss = append(ss, discover.ServiceInfo{
			ID:       e.Service.ID,
			Name:     e.Service.Service,
			Addr:     addr,
			Metadata: meta,
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
