package sketch

import (
	"errors"
	"math"
	"math/bits"
)

// HyperLogLog 估计集合基数(去重元素个数)。用 m=2^precision 个 6-bit 寄存器(这里每个用一个
// uint8)记录哈希尾部前导零的最大值,据此估算基数。precision=14 时约 16KB 内存,标准误差 ~0.81%,
// 可估计到数十亿量级。并发不安全(需要并发时由调用方加锁,或每 goroutine 一个再 Merge)。
type HyperLogLog struct {
	p   uint8
	m   uint32
	reg []uint8
}

// NewHyperLogLog 创建 HLL。precision 取值 [4,16],越大越精确也越占内存(寄存器数=2^precision)。
// 越界会被夹到区间内;precision=0 用默认 14。
func NewHyperLogLog(precision int) *HyperLogLog {
	p := precision
	if p == 0 {
		p = 14
	}
	if p < 4 {
		p = 4
	}
	if p > 16 {
		p = 16
	}
	m := uint32(1) << uint(p)
	return &HyperLogLog{p: uint8(p), m: m, reg: make([]uint8, m)}
}

// AddString 加入一个字符串元素。
func (h *HyperLogLog) AddString(s string) { h.AddHash(hashString(s)) }

// AddHash 加入一个已算好的 64 位哈希(调用方自带高质量哈希时用)。
func (h *HyperLogLog) AddHash(x uint64) {
	idx := x >> (64 - h.p) // 高 p 位作寄存器下标
	// 剩余位左移到高位,再置一个保护位,保证 rho 有上界(<= 64-p+1)。
	w := (x << h.p) | (1 << (h.p - 1))
	rho := uint8(bits.LeadingZeros64(w)) + 1
	if rho > h.reg[idx] {
		h.reg[idx] = rho
	}
}

// Count 返回当前基数估计。
func (h *HyperLogLog) Count() uint64 {
	m := float64(h.m)
	sum := 0.0
	zeros := 0
	for _, v := range h.reg {
		sum += 1.0 / float64(uint64(1)<<v)
		if v == 0 {
			zeros++
		}
	}
	est := alpha(h.m) * m * m / sum
	// 小基数用线性计数更准(避免 HLL 在稀疏时的偏差);64 位哈希无需大范围修正。
	if est <= 2.5*m && zeros > 0 {
		est = m * math.Log(m/float64(zeros))
	}
	return uint64(est + 0.5)
}

// Merge 把 other 并入当前 HLL(逐寄存器取最大)。要求二者 precision 相同。
func (h *HyperLogLog) Merge(other *HyperLogLog) error {
	if h.p != other.p {
		return errors.New("sketch: HyperLogLog precision 不一致,无法 Merge")
	}
	for i, v := range other.reg {
		if v > h.reg[i] {
			h.reg[i] = v
		}
	}
	return nil
}

// Reset 清空,可复用。
func (h *HyperLogLog) Reset() {
	for i := range h.reg {
		h.reg[i] = 0
	}
}

// alpha 是 HLL 的偏差修正常数。
func alpha(m uint32) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/float64(m))
	}
}
