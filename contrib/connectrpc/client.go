package connectrpc

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/store/loadbalance"
)

type serviceNode struct {
	info discover.ServiceInfo
}

func (n serviceNode) ID() string { return n.info.ID }
func (n serviceNode) Weight() int {
	if w, ok := n.info.Metadata["weight"]; ok {
		if v, err := strconv.Atoi(w); err == nil && v > 0 {
			return v
		}
	}
	return 100
}

// Transport 是支持服务发现的 http.RoundTripper，用于 Connect 客户端。
//
// 它从 discover.Discovery 获取服务实例列表，通过轮询负载均衡选择实例，
// 将请求转发到对应地址。配合 protoc-gen-connect-go 生成的客户端使用：
//
//	rt := connectrpc.NewTransport(discovery, "my.service.v1.MyService")
//	defer rt.Close()
//	httpClient := &http.Client{Transport: rt}
//	client := myv1connect.NewMyServiceClient(httpClient, "http://my.service.v1.MyService/")
type Transport struct {
	discovery   discover.Discovery
	serviceName string
	base        http.RoundTripper

	mu sync.RWMutex
	rr *loadbalance.RoundRobin[serviceNode]

	ctx    context.Context
	cancel context.CancelFunc

	refreshInterval time.Duration
}

// TransportOption 是 Transport 的功能选项。
type TransportOption func(*Transport)

// WithBaseTransport 设置底层 http.RoundTripper，默认 http.DefaultTransport。
func WithBaseTransport(rt http.RoundTripper) TransportOption {
	return func(t *Transport) {
		t.base = rt
	}
}

// WithRefreshInterval 设置 Watch 失败时的兜底轮询间隔，默认 10s。
func WithRefreshInterval(d time.Duration) TransportOption {
	return func(t *Transport) {
		t.refreshInterval = d
	}
}

// NewTransport 创建支持服务发现的 HTTP Transport。
// serviceName 为 protobuf 服务全限定名（如 "my.service.v1.MyService"），
// 与注册中心中的服务名一致。
//
// Transport 会优先通过 Watch 实时感知实例变化；如果 Watch 不可用，
// 则按 refreshInterval 定时轮询。使用完毕后需调用 Close 释放资源。
func NewTransport(discovery discover.Discovery, serviceName string, opts ...TransportOption) *Transport {
	ctx, cancel := context.WithCancel(context.Background())
	t := &Transport{
		discovery:       discovery,
		serviceName:     serviceName,
		base:            http.DefaultTransport,
		ctx:             ctx,
		cancel:          cancel,
		refreshInterval: 10 * time.Second,
	}
	for _, o := range opts {
		o(t)
	}
	t.refresh()
	go t.watch()
	return t
}

func (t *Transport) refresh() {
	services, err := t.discovery.Find(t.ctx, t.serviceName)
	if err != nil {
		logger.Error("connect transport refresh failed",
			"service", t.serviceName, "error", err)
		return
	}
	t.updateNodes(services)
}

func (t *Transport) updateNodes(services []discover.ServiceInfo) {
	nodes := make([]serviceNode, len(services))
	for i, svc := range services {
		nodes[i] = serviceNode{info: svc}
	}
	t.mu.Lock()
	t.rr = loadbalance.NewRoundRobin(nodes)
	t.mu.Unlock()
}

func (t *Transport) watch() {
	err := t.discovery.Watch(t.ctx, t.serviceName, func(services []discover.ServiceInfo) {
		t.updateNodes(services)
	})
	if err != nil {
		ticker := time.NewTicker(t.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-t.ctx.Done():
				return
			case <-ticker.C:
				t.refresh()
			}
		}
	}
}

// RoundTrip 实现 http.RoundTripper 接口。将请求的 Host 替换为
// 负载均衡选中的实例地址，然后转发给底层 Transport。
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.RLock()
	rr := t.rr
	t.mu.RUnlock()

	if rr == nil {
		return nil, fmt.Errorf("connectrpc: no available instances for %s", t.serviceName)
	}

	node, ok := rr.Next()
	if !ok {
		return nil, fmt.Errorf("connectrpc: no available instances for %s", t.serviceName)
	}

	clone := req.Clone(req.Context())
	clone.URL.Host = node.info.Addr
	clone.Host = node.info.Addr
	if clone.URL.Scheme == "" {
		clone.URL.Scheme = "http"
	}

	return t.base.RoundTrip(clone)
}

// Close 释放 Transport 持有的资源（停止 Watch/轮询）。
func (t *Transport) Close() {
	t.cancel()
}
