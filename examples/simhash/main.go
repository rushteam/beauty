// simhash 示例:SimHash / MinHash 近似相似去重。
//
// 演示 pkg/foundation/simhash:判「有没有见过差不多的」(而非完全一样)。
// 场景:近重复网页/文案去重、爬虫判重、评论聚类、集合相似度。
package main

import (
	"fmt"
	"strings"

	"github.com/rushteam/beauty/pkg/foundation/simhash"
)

func main() {
	// ===== SimHash:文档近重复 =====
	fmt.Println("== SimHash 文档近重复 ==")
	docs := map[string]string{
		"原文":  "the quick brown fox jumps over the lazy dog",
		"改一词": "the quick brown fox jumps over the lazy cat",
		"无关":  "machine learning models require large training datasets",
	}
	base := simhash.Hash(strings.Fields(docs["原文"])...)
	for name, text := range docs {
		h := simhash.Hash(strings.Fields(text)...)
		fmt.Printf("  %-5s 汉明距离=%2d 相似度=%.2f 近重复?%v\n",
			name, simhash.Distance(base, h), simhash.Similarity(base, h), simhash.Similar(base, h, 3))
	}

	// ===== MinHash:集合 Jaccard 相似度 =====
	fmt.Println("\n== MinHash 集合相似度 ==")
	mh := simhash.NewMinHash(256)
	cartA := []string{"牛奶", "面包", "鸡蛋", "黄油", "咖啡"}
	cartB := []string{"牛奶", "面包", "鸡蛋", "黄油", "茶叶"} // 4/6 相同
	cartC := []string{"螺丝", "扳手", "电钻"}
	sa, sb, sc := mh.Signature(cartA), mh.Signature(cartB), mh.Signature(cartC)
	fmt.Printf("  购物篮 A vs B 相似度 ≈ %.2f (真值 4/6≈0.67)\n", simhash.Jaccard(sa, sb))
	fmt.Printf("  购物篮 A vs C 相似度 ≈ %.2f (几乎不相关)\n", simhash.Jaccard(sa, sc))
}
