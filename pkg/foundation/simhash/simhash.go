// Package simhash 提供两种「近似相似/去重」指纹算法:SimHash 与 MinHash。它们回答的是
// 「两份内容有多像」——布隆/布谷鸟过滤器判「见没见过完全一样的」,而这里判「有没有见过
// 差不多的」:近重复网页/文案去重、爬虫判重、评论/垃圾内容聚类、推荐去冗余。
//
//   - SimHash:把一段内容(一组带权特征/token)压成一个 64 位指纹,内容相似则指纹的
//     汉明距离(不同 bit 数)小。指纹定长 8 字节、可直接入库/建索引,比对是一次异或 + popcount,
//     适合海量文档两两近重复判定。
//   - MinHash(见 minhash.go):用 k 个最小哈希签名估计两个**集合**的 Jaccard 相似度
//     (交/并),适合「集合型」对象(用户标签集、购物篮、文档 shingle 集)的相似度/聚类。
//
// 纯标准库、无状态(SimHash)或确定性参数(MinHash,同 k 的签名跨进程可比)。并发安全。
package simhash

import "math/bits"

// Hash 计算一组特征 token 的 64 位 SimHash 指纹(每个 token 权重 1)。
func Hash(tokens ...string) uint64 {
	var v [64]int
	for _, tk := range tokens {
		accumulate(&v, hash64(tk), 1)
	}
	return collapse(&v)
}

// HashWeighted 计算带权特征的 SimHash:权重越大的特征对指纹影响越大(如 TF-IDF 权重、
// 标题 token 加权)。weights 的 value 为该 token 的权重(可正可负,负权表示抑制)。
func HashWeighted(weights map[string]int) uint64 {
	var v [64]int
	for tk, w := range weights {
		accumulate(&v, hash64(tk), w)
	}
	return collapse(&v)
}

// accumulate 把一个 token 的哈希按权重投票到 64 个计数桶:bit 为 1 加权重,为 0 减权重。
func accumulate(v *[64]int, h uint64, w int) {
	for i := 0; i < 64; i++ {
		if h&(1<<uint(i)) != 0 {
			v[i] += w
		} else {
			v[i] -= w
		}
	}
}

// collapse 把投票结果塌缩成指纹:正票为 1。
func collapse(v *[64]int) uint64 {
	var f uint64
	for i := 0; i < 64; i++ {
		if v[i] > 0 {
			f |= 1 << uint(i)
		}
	}
	return f
}

// Distance 返回两个指纹的汉明距离(不同 bit 数,0..64)。越小越相似。
func Distance(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// Similarity 返回两个指纹的相似度 = 1 - 距离/64,落在 [0,1]。
func Similarity(a, b uint64) float64 { return 1 - float64(Distance(a, b))/64 }

// Similar 判断两个指纹是否在给定汉明距离阈值内(经验上 64 位指纹 <=3 视为近重复)。
func Similar(a, b uint64, maxDistance int) bool { return Distance(a, b) <= maxDistance }

// hash64 用 FNV-1a + splitmix64 收敛器计算 64 位哈希(确定性,跨进程一致)。
func hash64(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	// splitmix64 finalizer
	h += 0x9e3779b97f4a7c15
	h = (h ^ (h >> 30)) * 0xbf58476d1ce4e5b9
	h = (h ^ (h >> 27)) * 0x94d049bb133111eb
	return h ^ (h >> 31)
}
