// Package filter 提供两种「近似集合」概率过滤器,纯标准库、纯内存实现:
//
//   - BloomFilter:布隆过滤器。用极小内存快速判断"元素一定不存在 / 可能存在",
//     只会假阳性(误报存在)、绝不假阴性(漏报);不支持删除、不可枚举;
//   - CuckooFilter:布谷鸟过滤器。同样是概率型判存,但**支持删除**,且在低负载下
//     空间效率与查询局部性通常优于同等误判率的布隆过滤器。
//
// 与相邻包的分工:
//   - pkg/utils/bloom 是"外部存储版"(依赖 Redis bitfield 的 Store 接口,跨实例共享);
//     本包是"纯内存版",零依赖、无网络往返,适合进程内热路径(缓存穿透前置判断、
//     海量去重、爬虫 URL 判重、垃圾邮件指纹);
//   - pkg/foundation/bitmap 是精确型(位与稠密 ID 一一对应,可精确计数/枚举);
//     本包是概率型(用哈希把任意 key 压到固定位空间,省内存但有误判);
//   - pkg/foundation/sketch 估计的是"基数/频率"(HLL/CountMin),本包判定的是"存在性"。
//
// 选择建议:只判存在、不删除、追求最省内存 → Bloom;需要删除元素 → Cuckoo。
//
// 并发安全:两者均非并发安全(读多写少可在上层加锁,或每个写者独占一份)。
// 零值不可用,用 NewBloom / NewCuckoo 构造。
package filter

// hashBytes 用 FNV-1a 计算 64 位哈希(确定性,不依赖随机种子,故过滤器可序列化/跨进程复用)。
func hashBytes(b []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	return h
}

// mix64 是 splitmix64 的 finalizer,用于从单个哈希派生第二个"独立"哈希(双重散列),
// 改善位分布,避免 FNV 单独用于多点定位时的相关性。
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
