// Package sketch 提供监控/风控常用的概率数据结构:用极小、常量级内存换取"近似但够用"的答案。
//
//   - HyperLogLog:基数估计(去重计数)——UV、独立 IP、活跃用户;几 KB 估计到百万级,误差 ~1%;
//   - CountMin:频率估计——热 key / Top-N / 限流分桶;常量内存,只会高估不会低估;
//   - Reservoir:蓄水池采样——从未知长度的流里等概率抽 K 条(日志/trace 采样、限流下采样)。
//
// 均为纯标准库。哈希用固定的 FNV-1a + splitmix64 收敛器(确定性,故 HLL 可跨实例 Merge)。
package sketch

// hashString 用 FNV-1a 计算 64 位哈希,再过 splitmix64 收敛器改善位分布(FNV 单独用于 HLL/CMS
// 的雪崩性偏弱)。确定性:同输入恒得同值,不依赖随机种子。
func hashString(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return mix64(h)
}

// mix64 是 splitmix64 的收敛(finalizer)函数,把输入充分打散。
func mix64(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
