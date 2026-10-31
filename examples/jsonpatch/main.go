// jsonpatch 示例:结构化 JSON diff / RFC6902 补丁 / 三路合并。
//
// 演示 pkg/foundation/diff 的结构化能力(在按行 diff 之外,见 examples/diff)。
// 场景:配置中心按字段下发、API 局部更新、协同编辑的分支合并。
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rushteam/beauty/pkg/foundation/diff"
)

func mustJSON(s string) any {
	var v any
	_ = json.Unmarshal([]byte(s), &v)
	return v
}

func main() {
	// ===== 结构化 JSON diff + RFC6902 补丁 =====
	fmt.Println("== 配置变更:只下发变化的字段 ==")
	oldCfg := mustJSON(`{"timeout":30,"retries":3,"tags":["a","b"],"db":{"host":"h1","pool":10}}`)
	newCfg := mustJSON(`{"timeout":60,"retries":3,"tags":["a","b","c"],"db":{"host":"h1","pool":20}}`)

	patch := diff.JSONDiff(oldCfg, newCfg)
	for _, op := range patch {
		b, _ := json.Marshal(op)
		fmt.Printf("  %s\n", b)
	}
	// 把补丁应用回旧配置,得到新配置。
	applied, _ := diff.ApplyJSON(oldCfg, patch)
	ok, _ := json.Marshal(applied)
	fmt.Printf("  应用补丁后 == 新配置:%s\n\n", ok)

	// ===== 三路合并:两人并发改同一份配置 =====
	fmt.Println("== 三路合并(两个分支汇流) ==")
	base := "name: svc\nreplicas: 2\nport: 8080"
	mine := "name: svc\nreplicas: 5\nport: 8080"  // 我改了副本数
	yours := "name: svc\nreplicas: 2\nport: 9090" // 他改了端口
	merged, conflicts := diff.Merge3Lines(base, mine, yours)
	fmt.Printf("  合并结果:\n%s\n", "    "+strings.ReplaceAll(merged, "\n", "\n    "))
	fmt.Printf("  冲突数:%d(两处改动不相干,自动合并)\n", len(conflicts))
}
