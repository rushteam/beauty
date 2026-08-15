package agones

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	allocation "agones.dev/agones/pkg/allocation/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// AllocationRequest 描述一次 GameServer 分配请求。
type AllocationRequest struct {
	Namespace string
	// GameServerSelectors 为空时分配任意 Ready GameServer。
	GameServerSelectors []*allocation.GameServerSelector
	Metadata            map[string]string
}

// AllocationResult 是分配到的 GameServer 连接信息。
type AllocationResult struct {
	GameServerName string
	Address        string // host:port(优先 External/Address 字段)
}

// Allocator 从 Agones 集群(或 mock)分配 GameServer 地址。
type Allocator interface {
	Allocate(ctx context.Context, req AllocationRequest) (AllocationResult, error)
}

// PoolAllocator 本地地址池轮询(mock,无需 K8s)。
type PoolAllocator struct {
	addrs []string
	idx   atomic.Uint64
}

// NewPoolAllocator 创建地址池分配器。
func NewPoolAllocator(addrs []string) *PoolAllocator {
	if len(addrs) == 0 {
		addrs = []string{"127.0.0.1:8130"}
	}
	return &PoolAllocator{addrs: addrs}
}

// Allocate 轮询返回池中地址。
func (p *PoolAllocator) Allocate(_ context.Context, _ AllocationRequest) (AllocationResult, error) {
	i := p.idx.Add(1)
	addr := p.addrs[int(i-1)%len(p.addrs)]
	return AllocationResult{GameServerName: "pool-" + addr, Address: addr}, nil
}

// GRPCAllocator 通过 agones-allocator gRPC 服务分配 GameServer。
type GRPCAllocator struct {
	client    allocation.AllocationServiceClient
	conn      *grpc.ClientConn
	namespace string
}

// GRPCOption 配置 GRPCAllocator。
type GRPCOption func(*grpcAllocatorConfig)

type grpcAllocatorConfig struct {
	namespace string
	tlsConfig *tls.Config
	insecure  bool
}

// WithAllocatorNamespace 设置默认 namespace(默认 "default")。
func WithAllocatorNamespace(ns string) GRPCOption {
	return func(c *grpcAllocatorConfig) { c.namespace = ns }
}

// WithAllocatorTLS 设置 mTLS 配置(生产 Agones Allocator 常用)。
func WithAllocatorTLS(cfg *tls.Config) GRPCOption {
	return func(c *grpcAllocatorConfig) { c.tlsConfig = cfg }
}

// WithAllocatorInsecure 跳过 TLS(仅本地联调)。
func WithAllocatorInsecure() GRPCOption {
	return func(c *grpcAllocatorConfig) { c.insecure = true }
}

// NewGRPCAllocator 连接 agones-allocator 端点(如 allocator.agones-system:443)。
func NewGRPCAllocator(target string, opts ...GRPCOption) (*GRPCAllocator, error) {
	if target == "" {
		return nil, fmt.Errorf("agones: empty allocator target")
	}
	cfg := grpcAllocatorConfig{namespace: "default"}
	for _, o := range opts {
		o(&cfg)
	}
	var creds credentials.TransportCredentials
	switch {
	case cfg.tlsConfig != nil:
		creds = credentials.NewTLS(cfg.tlsConfig)
	case cfg.insecure:
		creds = insecure.NewCredentials()
	default:
		return nil, fmt.Errorf("agones: allocator requires WithAllocatorTLS or WithAllocatorInsecure")
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("agones: dial allocator: %w", err)
	}
	return &GRPCAllocator{
		client:    allocation.NewAllocationServiceClient(conn),
		conn:      conn,
		namespace: cfg.namespace,
	}, nil
}

// Close 关闭 gRPC 连接。
func (a *GRPCAllocator) Close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Allocate 调用 AllocationService.Allocate。
func (a *GRPCAllocator) Allocate(ctx context.Context, req AllocationRequest) (AllocationResult, error) {
	ns := req.Namespace
	if ns == "" {
		ns = a.namespace
	}
	grpcReq := &allocation.AllocationRequest{Namespace: ns}
	if len(req.GameServerSelectors) > 0 {
		grpcReq.GameServerSelectors = req.GameServerSelectors
	}
	if len(req.Metadata) > 0 {
		grpcReq.Metadata = &allocation.MetaPatch{Labels: req.Metadata}
	}
	resp, err := a.client.Allocate(ctx, grpcReq)
	if err != nil {
		return AllocationResult{}, fmt.Errorf("agones: allocate: %w", err)
	}
	addr := formatGameServerAddress(resp.GetAddress(), resp.GetPorts())
	if addr == "" {
		addr = formatGameServerAddress(pickAddress(resp.GetAddresses()), resp.GetPorts())
	}
	if addr == "" {
		return AllocationResult{}, fmt.Errorf("agones: empty address in allocation response")
	}
	return AllocationResult{
		GameServerName: resp.GetGameServerName(),
		Address:        addr,
	}, nil
}

func formatGameServerAddress(host string, ports []*allocation.AllocationResponse_GameServerStatusPort) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	port := pickGamePort(ports)
	if port <= 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func pickGamePort(ports []*allocation.AllocationResponse_GameServerStatusPort) int32 {
	for _, pref := range []string{"default", "game", "gameserver"} {
		for _, p := range ports {
			if strings.EqualFold(p.GetName(), pref) && p.GetPort() > 0 {
				return p.GetPort()
			}
		}
	}
	for _, p := range ports {
		if p.GetPort() > 0 {
			return p.GetPort()
		}
	}
	return 0
}

func pickAddress(addrs []*allocation.AllocationResponse_GameServerStatusAddress) string {
	for _, pref := range []string{"External", "ExternalDNS", "Unicast"} {
		for _, a := range addrs {
			if a.GetType() == pref && a.GetAddress() != "" {
				return a.GetAddress()
			}
		}
	}
	for _, a := range addrs {
		if a.GetAddress() != "" {
			return a.GetAddress()
		}
	}
	return ""
}

// TLSConfigFromFiles 从 cert/key/ca 文件构建 mTLS 配置(Agones Allocator 客户端)。
func TLSConfigFromFiles(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("agones: invalid CA PEM")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
