// loadbalance 示例:一致性哈希(会话粘性)+ 平滑加权轮询(按容量分发)。
//
// 演示 pkg/loadbalance 的两个算法:
//   - /consistent?user=alice  按用户名一致性哈希路由到稳定后端;
//   - /wrr                     加权轮询,每次请求按权重比例选后端。
//
// 跑:go run ./examples/loadbalance
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/loadbalance"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

// backend 模拟一个后端节点。
type backend struct {
	id     string
	weight int
	hits   atomic.Int64
}

func (b *backend) ID() string  { return b.id }
func (b *backend) Weight() int { return b.weight }

func main() {
	// 三个后端,权重 1:2:3(模拟不同容量)。
	backends := []*backend{
		{id: "node-a", weight: 1},
		{id: "node-b", weight: 2},
		{id: "node-c", weight: 3},
	}
	// ConsistentHash/WRR 的泛型参数是 *backend(实现 Node 接口)。
	var nodes []*backend = backends // 推断 T = *backend

	// 一致性哈希:按 key(用户名)稳定路由到同一后端(会话粘性 / 带状态分片)。
	ch := loadbalance.NewConsistentHash(nodes, loadbalance.WithVirtualFactor[*backend](150))

	// 平滑加权轮询:按权重比例均匀分发(避免低权重节点被连续命中)。
	wrr := loadbalance.NewWeightedRoundRobin(nodes)

	mux := http.NewServeMux()

	// /consistent?user=alice —— 同一用户总是命中同一后端。
	mux.HandleFunc("/consistent", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if user == "" {
			user = "anonymous"
		}
		got, ok := ch.Get(user)
		if !ok {
			http.Error(w, "no backend", http.StatusServiceUnavailable)
			return
		}
		got.hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": user, "backend": got.id, "hits": got.hits.Load(),
		})
	})

	// /wrr —— 每次请求按权重轮询选后端。
	mux.HandleFunc("/wrr", func(w http.ResponseWriter, r *http.Request) {
		got, ok := wrr.Next()
		if !ok {
			http.Error(w, "no backend", http.StatusServiceUnavailable)
			return
		}
		got.hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"backend": got.id, "hits": got.hits.Load(),
		})
	})

	// /stats —— 查看各后端累计命中数(验证分布)。
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]int64{}
		for _, b := range backends {
			out[b.id] = b.hits.Load()
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	app := beauty.New(
		beauty.WithWebServer(":8306", mux,
			webserver.WithServiceName("loadbalance-demo"),
		),
	)
	fmt.Println("loadbalance demo: http://localhost:8306")
	fmt.Println("  consistent: curl 'http://localhost:8306/consistent?user=alice'")
	fmt.Println("  wrr:        curl http://localhost:8306/wrr")
	fmt.Println("  stats:      curl http://localhost:8306/stats")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
