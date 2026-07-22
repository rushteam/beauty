package cache_test

import (
	"fmt"
	"testing"

	"github.com/rushteam/beauty/pkg/cache"
)

func TestLRU_Basic(t *testing.T) {
	c := cache.NewLRU[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("a = %v,%v", v, ok)
	}
	// 容量 2,写入 c → 淘汰最久未使用的 b(a 刚被访问)。
	c.Set("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b 应被淘汰")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a 应仍在")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c 应在")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d", c.Len())
	}
}

func TestLRU_UpdateAndDelete(t *testing.T) {
	c := cache.NewLRU[string, int](2)
	c.Set("a", 1)
	c.Set("a", 10) // 更新不增容量
	if v, _ := c.Get("a"); v != 10 {
		t.Fatalf("a = %d", v)
	}
	if c.Len() != 1 {
		t.Fatalf("更新不应增容量, Len=%d", c.Len())
	}
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("删除后 a 应不存在")
	}
}

// LRU 容量恒不超限。
func TestLRU_BoundedLen(t *testing.T) {
	c := cache.NewLRU[int, int](50)
	for i := 0; i < 1000; i++ {
		c.Set(i, i)
		if c.Len() > 50 {
			t.Fatalf("Len 超过容量: %d", c.Len())
		}
	}
}

// TinyLFU:容量恒不超限。
func TestTinyLFU_BoundedLen(t *testing.T) {
	c := cache.NewTinyLFU[int, int](100)
	for i := 0; i < 5000; i++ {
		c.Set(i, i)
		if c.Len() > 100 {
			t.Fatalf("Len 超过容量: %d", c.Len())
		}
	}
}

// TinyLFU 核心特性:高频热点能抵御一次性冷 key 的冲刷(抗扫描)。
// 冲刷规模控制在老化窗口内(< sampleSize),使一次性 key 频率保持 ~1、而 hot 频率高,准入拒绝冷 key。
func TestTinyLFU_FrequencyAdmission(t *testing.T) {
	c := cache.NewTinyLFU[string, int](100)
	c.Set("hot", 1)
	for i := 0; i < 100; i++ { // 把 hot 频率打高(饱和)
		c.Get("hot")
	}
	// 涌入超过容量、但不触发老化的一次性冷 key(每个只出现一次)。
	for i := 0; i < 500; i++ {
		c.Set(fmt.Sprintf("cold-%d", i), i)
	}
	if _, ok := c.Get("hot"); !ok {
		t.Fatal("高频 hot 应在冷 key 冲刷下存活(TinyLFU 频率准入)")
	}
}

// 纯 LRU 对比:同样冲刷下,未被再次访问的热点会被冷 key 挤掉(印证 TinyLFU 的价值)。
func TestLRU_HotEvictedByScan(t *testing.T) {
	c := cache.NewLRU[string, int](100)
	c.Set("hot", 1)
	for i := 0; i < 100; i++ {
		c.Get("hot")
	}
	for i := 0; i < 500; i++ {
		c.Set(fmt.Sprintf("cold-%d", i), i)
	}
	if _, ok := c.Get("hot"); ok {
		t.Fatal("纯 LRU 下 hot 预期被冷 key 挤掉")
	}
}
