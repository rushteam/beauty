package bloom

import (
	"context"
	"crypto/sha256"
	"math"
)

type Store interface {
	SetBit(ctx context.Context, key string, offset int64, value int) (int64, error)
	GetBit(ctx context.Context, key string, offset int64) (int64, error)
}

// 布隆过滤器结构体
type BloomFilter struct {
	store     Store
	bitmapKey string
	size      uint
	hashCount uint
}

// 初始化布隆过滤器
func New(store Store, bitmapKey string, expectedElements uint, falsePositiveRate float64) *BloomFilter {
	size := optimalSize(expectedElements, falsePositiveRate)
	hashCount := optimalHashCount(expectedElements, size)

	return &BloomFilter{
		store:     store,
		bitmapKey: bitmapKey,
		size:      size,
		hashCount: hashCount,
	}
}

// 计算布隆过滤器的最佳大小（位数组长度）
func optimalSize(n uint, p float64) uint {
	return uint(-float64(n) * math.Log(p) / (math.Pow(math.Ln2, 2)))
}

// 计算布隆过滤器的最佳哈希函数数量
func optimalHashCount(n, m uint) uint {
	return uint(math.Ln2 * float64(m) / float64(n))
}

// 哈希函数生成多个索引
func (bf *BloomFilter) getHashes(data string) []uint {
	hashes := []uint{}
	hash1 := sha256.Sum256([]byte(data))
	for i := uint(0); i < bf.hashCount; i++ {
		combined := append(hash1[:], byte(i)) // 每次改变哈希函数的输入
		hash2 := sha256.Sum256(combined)
		idx := uint(hash2[0]) % bf.size // 对过滤器大小取模
		hashes = append(hashes, idx)
	}
	return hashes
}

// 添加元素到布隆过滤器
func (bf *BloomFilter) Add(data string) error {
	hashes := bf.getHashes(data)
	// 使用 Redis 的 SETBIT 操作将哈希索引位置的位设置为 1
	for _, hash := range hashes {
		_, err := bf.store.SetBit(ctx, bf.bitmapKey, int64(hash), 1)
		if err != nil {
			return err
		}
	}
	return nil
}

// 检查元素是否在布隆过滤器中
func (bf *BloomFilter) Test(data string) (bool, error) {
	hashes := bf.getHashes(data)

	// 使用 Redis 的 GETBIT 检查所有哈希索引位置的位是否都为 1
	for _, hash := range hashes {
		bit, err := bf.store.GetBit(ctx, bf.bitmapKey, int64(hash))
		if err != nil {
			return false, err
		}
		if bit == 0 {
			return false, nil
		}
	}
	return true, nil
}
