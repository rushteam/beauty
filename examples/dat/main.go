// dat 示例:双数组 Trie / 双数组压缩 AC 自动机。
//
// 演示 pkg/foundation/actrie 的双数组结构(在 map 版 Matcher 之外,见 examples/actrie):
//   - DoubleArrayTrie:静态只读词典的精确匹配 / 最长前缀匹配(分词词典、路由表);
//   - DoubleArrayAC:双数组压缩的多模匹配(百万级敏感词只读大词典,极致省内存)。
package main

import (
	"fmt"
	"unicode/utf8"

	"github.com/rushteam/beauty/pkg/foundation/actrie"
)

func main() {
	// ===== 双数组 Trie:分词词典正向最大匹配 =====
	fmt.Println("== DoubleArrayTrie 词典分词 ==")
	dict := actrie.BuildDoubleArraySet([]string{"北京", "北京大学", "大学", "生活", "学生"})
	text := "北京大学的学生"
	fmt.Printf("  对 %q 做正向最大匹配:\n", text)
	for i := 0; i < len(text); {
		if prefix, _, ok := dict.LongestPrefix(text[i:]); ok {
			fmt.Printf("    切出词:%s\n", prefix)
			i += len(prefix)
		} else {
			// 未成词:按单个 UTF-8 字符跳过。
			_, size := utf8.DecodeRuneInString(text[i:])
			fmt.Printf("    单字:%s\n", text[i:i+size])
			i += size
		}
	}

	// ===== 双数组 AC:多模敏感词扫描 =====
	fmt.Println("\n== DoubleArrayAC 敏感词扫描 ==")
	ac := actrie.BuildDoubleArrayAC("违禁", "敏感词", "测试")
	content := "这段文本含有违禁和敏感词需要过滤"
	fmt.Printf("  扫描 %q:\n", content)
	for _, m := range ac.FindAll(content) {
		fmt.Printf("    命中 %q at [%d,%d)\n", m.Pattern, m.Start, m.End)
	}
	fmt.Printf("  是否含敏感内容:%v\n", ac.Contains(content))
}
