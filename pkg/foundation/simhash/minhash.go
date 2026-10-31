package simhash

import "math/bits"

// mhPrime 是梅森素数 2^61-1,作为 MinHash 线性同余哈希族 (a*x+b) mod p 的模数。
const mhPrime = (1 << 61) - 1

// MinHash 用 k 个最小哈希为**集合**生成定长签名,两个签名相同位置相等的比例即估计两集合的
// Jaccard 相似度(|A∩B| / |A∪B|)。签名长度 k:越大越准(标准差 ~1/√k),也越占空间。
//
// 哈希族参数由 k 确定性派生(不含随机种子),故相同 k 的 MinHash 在不同实例/进程间产生的
// 签名可直接比较——适合各节点各自算签名、汇总比相似度。零值不可用,用 NewMinHash 构造。
type MinHash struct {
	k    int
	a, b []uint64
}

// NewMinHash 创建 k 哈希的 MinHash(k<=0 取 128)。
func NewMinHash(k int) *MinHash {
	if k <= 0 {
		k = 128
	}
	m := &MinHash{k: k, a: make([]uint64, k), b: make([]uint64, k)}
	for i := 0; i < k; i++ {
		// 由下标确定性派生 (a,b):a∈[1,p-1],b∈[0,p-1]。
		s1 := splitmix(uint64(i)*2 + 1)
		s2 := splitmix(uint64(i)*2 + 2)
		m.a[i] = s1%(mhPrime-1) + 1
		m.b[i] = s2 % mhPrime
	}
	return m
}

// K 返回签名长度。
func (m *MinHash) K() int { return m.k }

// Signature 为字符串集合生成签名。重复元素不影响结果(集合语义)。
func (m *MinHash) Signature(items []string) []uint64 {
	sig := make([]uint64, m.k)
	for i := range sig {
		sig[i] = mhPrime // 初始化为上界
	}
	for _, it := range items {
		m.foldHash(sig, hash64(it)%mhPrime)
	}
	return sig
}

// SignatureHashes 为「已预先哈希好的元素」生成签名(调用方自备 64 位哈希,避免重复哈希)。
func (m *MinHash) SignatureHashes(hashes []uint64) []uint64 {
	sig := make([]uint64, m.k)
	for i := range sig {
		sig[i] = mhPrime
	}
	for _, h := range hashes {
		m.foldHash(sig, h%mhPrime)
	}
	return sig
}

// foldHash 把一个元素哈希折进签名:每个哈希槽取 (a_i*hv+b_i) mod p 的最小值。
func (m *MinHash) foldHash(sig []uint64, hv uint64) {
	for i := 0; i < m.k; i++ {
		v := addmod(mulmod(m.a[i], hv), m.b[i])
		if v < sig[i] {
			sig[i] = v
		}
	}
}

// Jaccard 由两个**同 k** 的签名估计 Jaccard 相似度 = 相等位置数 / k。长度不等返回 0。
func Jaccard(sig1, sig2 []uint64) float64 {
	if len(sig1) == 0 || len(sig1) != len(sig2) {
		return 0
	}
	equal := 0
	for i := range sig1 {
		if sig1[i] == sig2[i] {
			equal++
		}
	}
	return float64(equal) / float64(len(sig1))
}

// mulmod 计算 (a*b) mod mhPrime,用 128 位中间积避免溢出(a,b<p ⇒ 高位 < p,除法合法)。
func mulmod(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	_, rem := bits.Div64(hi, lo, mhPrime)
	return rem
}

// addmod 计算 (x+b) mod mhPrime(x,b<p ⇒ 和 < 2p,一次减法即可)。
func addmod(x, b uint64) uint64 {
	s := x + b
	if s >= mhPrime {
		s -= mhPrime
	}
	return s
}

// splitmix 是 splitmix64 伪随机序列,用于确定性派生哈希族参数。
func splitmix(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
