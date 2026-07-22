package shard_test

import (
	"fmt"
	"testing"

	"github.com/rushteam/beauty/pkg/shard"
)

func TestJump_InRange(t *testing.T) {
	for i := 0; i < 1000; i++ {
		b := shard.Jump(uint64(i)*2654435761, 8)
		if b < 0 || b >= 8 {
			t.Fatalf("桶越界: key=%d bucket=%d", i, b)
		}
	}
}

func TestJump_Deterministic(t *testing.T) {
	if shard.Jump(12345, 16) != shard.Jump(12345, 16) {
		t.Fatal("同 key 同桶数应确定性一致")
	}
}

func TestJump_EdgeBuckets(t *testing.T) {
	if shard.Jump(999, 0) != 0 {
		t.Fatal("buckets<=0 应返回 0")
	}
	if shard.Jump(999, 1) != 0 {
		t.Fatal("buckets=1 只能返回 0")
	}
}

// 增桶时迁移量应约为 1/(n+1):从 n 到 n+1,只有落到新桶的 key 变化。
func TestJump_MinimalRemap(t *testing.T) {
	const n, total = 10, 100000
	moved := 0
	for i := 0; i < total; i++ {
		k := uint64(i) * 0x9e3779b97f4a7c15
		if shard.Jump(k, n) != shard.Jump(k, n+1) {
			moved++
		}
	}
	frac := float64(moved) / total
	want := 1.0 / float64(n+1) // ~0.0909
	if frac < want*0.8 || frac > want*1.2 {
		t.Fatalf("迁移比例应约 %.4f, got %.4f", want, frac)
	}
}

func members(ids ...string) []shard.Member {
	ms := make([]shard.Member, len(ids))
	for i, id := range ids {
		ms[i] = shard.StaticMember{NodeID: id, NodeWeight: 1}
	}
	return ms
}

func TestRendezvous_StableAndDistributed(t *testing.T) {
	r := shard.NewRendezvous(members("a", "b", "c", "d")...)

	// 确定性:同 key 恒同成员。
	m1, ok := r.Pick("session-42")
	if !ok {
		t.Fatal("应选出成员")
	}
	m2, _ := r.Pick("session-42")
	if m1.ID() != m2.ID() {
		t.Fatal("同 key 应稳定选同一成员")
	}

	// 分布:大量 key 应落到多个成员(不全挤在一个)。
	seen := map[string]int{}
	for i := 0; i < 10000; i++ {
		m, _ := r.Pick(fmt.Sprintf("k-%d", i))
		seen[m.ID()]++
	}
	if len(seen) != 4 {
		t.Fatalf("4 个成员都应分到 key, got %d", len(seen))
	}
}

// HRW 的关键性质:移除一个成员,只有原属于它的 key 重新分配,其余不变。
func TestRendezvous_MinimalDisruption(t *testing.T) {
	full := shard.NewRendezvous(members("a", "b", "c", "d")...)
	reduced := shard.NewRendezvous(members("a", "b", "c")...) // 去掉 d

	moved, onD := 0, 0
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("k-%d", i)
		before, _ := full.Pick(key)
		after, _ := reduced.Pick(key)
		if before.ID() == "d" {
			onD++
		}
		if before.ID() != after.ID() {
			moved++
		}
	}
	// 迁移的应恰好等于原本落在 d 上的(其余 key 归属不变)。
	if moved != onD {
		t.Fatalf("移除成员应只影响其自身的 key: moved=%d onD=%d", moved, onD)
	}
}

func TestRendezvous_Weighted(t *testing.T) {
	// 高权重成员应分到明显更多 key。
	r := shard.NewRendezvous(
		shard.StaticMember{NodeID: "big", NodeWeight: 10},
		shard.StaticMember{NodeID: "small", NodeWeight: 1},
	)
	seen := map[string]int{}
	for i := 0; i < 10000; i++ {
		m, _ := r.Pick(fmt.Sprintf("k-%d", i))
		seen[m.ID()]++
	}
	if seen["big"] <= seen["small"] {
		t.Fatalf("高权重应分到更多: big=%d small=%d", seen["big"], seen["small"])
	}
}

func TestRendezvous_PickN(t *testing.T) {
	r := shard.NewRendezvous(members("a", "b", "c", "d")...)
	top := r.PickN("key", 2)
	if len(top) != 2 {
		t.Fatalf("应返回 2 个, got %d", len(top))
	}
	// 第一个应与 Pick 一致。
	first, _ := r.Pick("key")
	if top[0].ID() != first.ID() {
		t.Fatal("PickN 首个应等于 Pick")
	}
}

func TestRendezvous_Empty(t *testing.T) {
	r := shard.NewRendezvous()
	if _, ok := r.Pick("x"); ok {
		t.Fatal("无成员应返回 false")
	}
}
