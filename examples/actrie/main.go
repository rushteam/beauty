// actrie 示例:敏感词过滤(AC 自动机)。
//
// 演示 pkg/foundation/actrie:一次扫描匹配多个敏感词;归一化对抗变体绕过;
// 泛型 Dict[T] 让每个词携带元数据(分类/等级),命中即可分级处置。
package main

import (
	"fmt"
	"unicode"

	"github.com/rushteam/beauty/pkg/foundation/actrie"
)

func main() {
	// ===== 1) 基础:多词一次扫描 + 等长遮罩 =====
	m := actrie.New()
	m.AddAll("敏感词", "违禁", "测试")
	text := "这段包含敏感词和违禁内容的测试文本"
	fmt.Println("== 基础匹配 ==")
	fmt.Printf("命中: ")
	for _, hit := range m.FindAll(text) {
		fmt.Printf("%q[%d,%d) ", hit.Pattern, hit.Start, hit.End)
	}
	fmt.Printf("\n脱敏: %s\n\n", m.Replace(text, '*'))

	// ===== 2) 反绕过:剥离干扰符 + 视觉映射 =====
	norm := actrie.Chain(
		actrie.Keep(func(r rune) bool { return unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r) }),
		actrie.VisualMap(),
	)
	mn := actrie.New(actrie.WithNormalizer(norm))
	mn.AddAll("敏感词", "color")
	fmt.Println("== 反绕过归一化 ==")
	for _, s := range []string{"敏-感-词", "c0l0r", "ＣＯＬＯＲ", "cоlоr(西里尔)"} {
		fmt.Printf("%-14s 命中? %v\n", s, mn.Contains(s))
	}
	fmt.Println()

	// ===== 3) 泛型 Dict[T]:词携带等级,分级遮罩 =====
	type meta struct {
		Category string
		Level    int
	}
	d := actrie.NewDict[meta]()
	d.Add("敏感", meta{Category: "政治", Level: 3})
	d.Add("广告", meta{Category: "营销", Level: 1})
	fmt.Println("== 泛型词典 + 分级处置 ==")
	out := d.ReplaceFunc("含敏感内容和广告", func(mt actrie.DictMatch[meta]) string {
		if mt.Payload.Level >= 3 {
			return "[已屏蔽]" // 高危词整段屏蔽
		}
		return "**" // 低危词打码
	})
	fmt.Printf("分级脱敏: %s\n", out)
}
