// http-service-discovery 示例:HTTP 客户端 + 服务发现 + 负载均衡。
//
// 演示 pkg/client/http 的 ServiceDiscoveryHTTPClient:
//   - 3 个 HTTP 后端(weight 1:2:3),注册到内存服务发现;
//   - 客户端用 WRR 策略,DoWith 一步调用(只给相对路径);
//   - NewRequest + Do 灵活形式(自定义 header);
//   - 打印每次命中的后端,验证按权重分布。
//
// 跑:go run ./examples/http-service-discovery
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"

	httpclient "github.com/rushteam/beauty/pkg/client/http"
	"github.com/rushteam/beauty/pkg/service/discover"
)

// memDiscovery 内存服务发现,实现 discover.Discovery。
// 仅供演示;生产用 etcd/nacos/consul 等实现。
type memDiscovery struct {
	mu       sync.RWMutex
	services []discover.ServiceInfo
	notify   discover.Notify
}

func (m *memDiscovery) Find(ctx context.Context, name string) ([]discover.ServiceInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]discover.ServiceInfo, len(m.services))
	copy(out, m.services)
	return out, nil
}

func (m *memDiscovery) Watch(ctx context.Context, name string, n discover.Notify) error {
	m.mu.Lock()
	m.notify = n
	m.mu.Unlock()
	n(m.services) // 立即推送初始列表
	<-ctx.Done()
	return ctx.Err()
}

func (m *memDiscovery) set(services []discover.ServiceInfo) {
	m.mu.Lock()
	m.services = services
	n := m.notify
	m.mu.Unlock()
	if n != nil {
		n(services)
	}
}

func main() {
	// 起三个 HTTP 后端,权重 1:2:3(模拟不同容量)。
	var hits [3]atomic.Int64
	newBackend := func(idx int, weight int) (*httptest.Server, discover.ServiceInfo) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits[idx].Add(1)
			fmt.Fprintf(w, "backend-%d", idx)
		}))
		return srv, discover.ServiceInfo{
			ID:   srv.Listener.Addr().String(),
			Addr: srv.Listener.Addr().String(),
			Metadata: map[string]string{
				"weight": fmt.Sprintf("%d", weight),
			},
		}
	}
	srv0, svc0 := newBackend(0, 1)
	srv1, svc1 := newBackend(1, 2)
	srv2, svc2 := newBackend(2, 3)
	defer srv0.Close()
	defer srv1.Close()
	defer srv2.Close()

	disc := &memDiscovery{services: []discover.ServiceInfo{svc0, svc1, svc2}}

	// 创建带服务发现的 HTTP 客户端,WRR 策略,不重试(演示用)。
	cli := httpclient.NewServiceDiscoveryHTTPClient(disc, "demo-svc",
		httpclient.WithHTTPStrategy(httpclient.HTTPWeightedRoundRobin),
		httpclient.WithHTTPMaxRetries(0),
	)
	ctx := context.Background()
	if err := cli.Start(ctx); err != nil {
		panic(err)
	}
	defer cli.Stop()

	fmt.Println("=== 便捷形式:DoWith(ctx, method, path, body) ===")
	for i := 1; i <= 6; i++ { // 一轮 6 次(1+2+3)
		resp, err := cli.DoWith(ctx, http.MethodGet, "/api/hello", nil)
		if err != nil {
			fmt.Printf("  req#%d error: %v\n", i, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("  req#%d -> %s\n", i, body)
	}

	fmt.Println("\n=== 灵活形式:NewRequest + Do(自定义 header) ===")
	req, _ := cli.NewRequest(ctx, http.MethodGet, "/api/hello")
	req.Header.Set("X-Trace-Id", "abc-123")
	resp, err := cli.Do(req)
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fmt.Printf("  -> %s (header sent: X-Trace-Id=abc-123)\n", body)
	}

	fmt.Println("\n=== 命中分布(weight 1:2:3) ===")
	for i := range hits {
		fmt.Printf("  backend-%d: %d hits\n", i, hits[i].Load())
	}
	// 预期:backend-0=1, backend-1=2, backend-2=3(一轮 SWRR 精确分配)
	// 加上灵活形式的 1 次(命中 backend-2,WRR 首选高权重),总计 1:2:4 附近。
}

// beauty 框架集成:实际项目用 beauty.New() 编排,这里简化为纯 client 演示。
// 若要嵌入 beauty app:
//
//	app := beauty.New()
//	app.AddService(cli) // 需 cli 实现 service.Service 接口(Start/Stop)
