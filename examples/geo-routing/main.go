// Package main 演示 LocalityRouter 地域亲和路由。
//
// 模拟三个不同地域的服务实例，展示 LocalityRouter 如何按 campus → zone → region
// 逐级 fallback 选择最近的实例。
//
// 使用方式:
//
//	go run ./examples/geo-routing
package main

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/rushteam/beauty/pkg/governance/router"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/utils/selector"
)

func main() {
	// 模拟注册中心中的服务实例（通常由 grpcserver.WithRegionInfo 写入 metadata）
	allNodes := []discover.ServiceInfo{
		{ID: "pay-1", Addr: "10.0.1.1:8080", Metadata: map[string]string{
			"region": "cn-east", "zone": "cn-east-1a", "campus": "campus-1", "version": "v2",
		}},
		{ID: "pay-2", Addr: "10.0.1.2:8080", Metadata: map[string]string{
			"region": "cn-east", "zone": "cn-east-1a", "campus": "campus-2", "version": "v2",
		}},
		{ID: "pay-3", Addr: "10.0.1.3:8080", Metadata: map[string]string{
			"region": "cn-east", "zone": "cn-east-1b", "campus": "campus-3", "version": "v1",
		}},
		{ID: "pay-4", Addr: "10.0.2.1:8080", Metadata: map[string]string{
			"region": "us-west", "zone": "us-west-1a", "campus": "campus-w1", "version": "v2",
		}},
	}

	slog.Info("所有服务实例", "count", len(allNodes))
	for _, n := range allNodes {
		slog.Info("  实例", "id", n.ID, "addr", n.Addr,
			"region", n.Metadata["region"],
			"zone", n.Metadata["zone"],
			"campus", n.Metadata["campus"])
	}
	fmt.Println(strings.Repeat("─", 60))

	// ─── 场景 1：campus 精确匹配 ───
	slog.Info("场景 1: campus 精确匹配")
	r1 := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
		Campus: "campus-1",
	})
	result := r1.Filter("payment", allNodes)
	printResult("campus-1 匹配", result)

	// ─── 场景 2：campus 无匹配，fallback 到 zone ───
	slog.Info("场景 2: campus 不存在，fallback 到 zone")
	r2 := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
		Campus: "campus-99", // 不存在
	})
	result = r2.Filter("payment", allNodes)
	printResult("zone cn-east-1a fallback", result)

	// ─── 场景 3：zone 也无匹配，fallback 到 region ───
	slog.Info("场景 3: zone 不存在，fallback 到 region")
	r3 := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-2x", // 不存在
		Campus: "campus-99",
	})
	result = r3.Filter("payment", allNodes)
	printResult("region cn-east fallback", result)

	// ─── 场景 4：全部不匹配，fail-closed ───
	slog.Info("场景 4: 全部不匹配 (fail-closed)")
	r4 := router.NewLocalityRouter(router.Locality{
		Region: "eu-west",
		Zone:   "eu-west-1a",
	})
	result = r4.Filter("payment", allNodes)
	printResult("无匹配 (globalFallback=false)", result)

	// ─── 场景 5：全部不匹配，globalFallback 回退全量 ───
	slog.Info("场景 5: 全部不匹配 + globalFallback=true")
	r5 := router.NewLocalityRouter(router.Locality{
		Region: "eu-west",
		Zone:   "eu-west-1a",
	}, router.WithGlobalFallback(true))
	result = r5.Filter("payment", allNodes)
	printResult("globalFallback 回退", result)

	// ─── 场景 6：ChainRouter 组合 locality + version 过滤 ───
	slog.Info("场景 6: ChainRouter (locality + version=v2)")
	locality := router.NewLocalityRouter(router.Locality{
		Region: "cn-east",
		Zone:   "cn-east-1a",
	}, router.WithGlobalFallback(true))
	versionFilter := router.NewLabelRouter(
		selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2"),
	)
	chain := router.NewChainRouter(locality, versionFilter)
	result = chain.Filter("payment", allNodes)
	printResult("locality+version chain", result)
}

func printResult(label string, nodes []discover.ServiceInfo) {
	if len(nodes) == 0 {
		slog.Info(fmt.Sprintf("  [%s] → 无匹配实例 (空)", label))
	} else {
		ids := make([]string, len(nodes))
		for i, n := range nodes {
			ids[i] = n.ID
		}
		slog.Info(fmt.Sprintf("  [%s] → %s", label, strings.Join(ids, ", ")))
	}
	fmt.Println(strings.Repeat("─", 60))
}
