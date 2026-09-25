// diff 示例:配置差异与补丁。
//
// 演示 pkg/foundation/diff:按行算最小编辑脚本(LCS),渲染差异,并把补丁应用回基线。
// 场景:配置中心动态下发(只推变更行)、操作审计、协同编辑、幂等回放校验。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/foundation/diff"
)

func main() {
	oldConf := "log_level = info\nmax_conn = 100\ntimeout = 30s\nretry = 3"
	newConf := "log_level = debug\nmax_conn = 100\ntimeout = 30s\nretry = 5\ntrace = on"

	patch := diff.DiffLines(oldConf, newConf)

	fmt.Println("== 完整差异(空=未变 -=删除 +=新增) ==")
	fmt.Print(diff.Format(patch))

	fmt.Println("\n== 仅变更行 ==")
	fmt.Print(diff.FormatCompact(patch))

	eq, ins, del := patch.Stats()
	fmt.Printf("\n统计: 保留 %d 行, 新增 %d 行, 删除 %d 行\n", eq, ins, del)

	// 把补丁应用回旧配置,应还原出新配置(可用于幂等下发校验)。
	got, err := diff.ApplyLines(oldConf, patch)
	if err != nil {
		fmt.Println("应用补丁失败:", err)
		return
	}
	fmt.Printf("\n应用补丁后 == 新配置? %v\n", got == newConf)

	// 基线漂移检测:配置在生成补丁后被别人改过,应用会报错而非静默出错。
	drifted := "completely different\nbaseline"
	if _, err := diff.ApplyLines(drifted, patch); err != nil {
		fmt.Printf("对错误基线应用补丁: 已拦截(%v)\n", err)
	}
}
