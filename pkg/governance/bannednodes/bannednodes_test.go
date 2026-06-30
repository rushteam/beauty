package bannednodes_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/governance/bannednodes"
)

func TestNotInjected_IsBannedFalse(t *testing.T) {
	// 未注入列表时 IsBanned 永远 false,零开销
	if bannednodes.IsBanned(context.Background(), "10.0.0.1:8080") {
		t.Fatal("uninjected ctx should not ban anything")
	}
	if addrs := bannednodes.BannedAddrs(context.Background()); addrs != nil {
		t.Fatalf("uninjected ctx should return nil, got %v", addrs)
	}
}

func TestBanAndIsBanned(t *testing.T) {
	ctx := bannednodes.WithBannedNodes(context.Background())
	if bannednodes.IsBanned(ctx, "a") {
		t.Fatal("a should not be banned before Ban")
	}
	bannednodes.Ban(ctx, "a", "b")
	if !bannednodes.IsBanned(ctx, "a") {
		t.Error("a should be banned after Ban")
	}
	if !bannednodes.IsBanned(ctx, "b") {
		t.Error("b should be banned after Ban")
	}
	if bannednodes.IsBanned(ctx, "c") {
		t.Error("c should not be banned")
	}
}

func TestBanDuplicate(t *testing.T) {
	ctx := bannednodes.WithBannedNodes(context.Background())
	bannednodes.Ban(ctx, "a")
	bannednodes.Ban(ctx, "a") // 幂等
	addrs := bannednodes.BannedAddrs(ctx)
	if len(addrs) != 1 {
		t.Errorf("duplicate ban want 1 addr, got %d: %v", len(addrs), addrs)
	}
}

func TestBannedAddrs_Snapshot(t *testing.T) {
	ctx := bannednodes.WithBannedNodes(context.Background())
	bannednodes.Ban(ctx, "a", "b", "c")
	addrs := bannednodes.BannedAddrs(ctx)
	if len(addrs) != 3 {
		t.Fatalf("want 3 addrs, got %d", len(addrs))
	}
	// 返回的是快照,修改不影响内部状态
	addrs[0] = "modified"
	if !bannednodes.IsBanned(ctx, "a") {
		t.Error("modifying snapshot should not affect internal state")
	}
}

func TestCtxIsolation(t *testing.T) {
	ctx1 := bannednodes.WithBannedNodes(context.Background())
	ctx2 := bannednodes.WithBannedNodes(context.Background())
	bannednodes.Ban(ctx1, "a")
	if !bannednodes.IsBanned(ctx1, "a") {
		t.Error("ctx1 should ban a")
	}
	if bannednodes.IsBanned(ctx2, "a") {
		t.Error("ctx2 should not see ctx1's ban")
	}
}

func TestCtxDerived(t *testing.T) {
	parent := bannednodes.WithBannedNodes(context.Background())
	bannednodes.Ban(parent, "a")
	child := context.WithValue(parent, "other", "val") // 派生 ctx 应继承
	if !bannednodes.IsBanned(child, "a") {
		t.Error("derived ctx should inherit parent's banned list")
	}
	bannednodes.Ban(child, "b")
	if !bannednodes.IsBanned(parent, "b") {
		t.Error("Ban on child should reflect to parent (shared list)")
	}
}

func TestBanOnUninjected_Noop(t *testing.T) {
	// 未注入时 Ban 静默忽略,不 panic
	bannednodes.Ban(context.Background(), "a", "b")
	if bannednodes.IsBanned(context.Background(), "a") {
		t.Error("Ban on uninjected ctx should be noop")
	}
}

func TestConcurrent(t *testing.T) {
	ctx := bannednodes.WithBannedNodes(context.Background())
	var wg sync.WaitGroup
	var errs atomic.Int64
	for range 10 {
		wg.Go(func() {
			for i := range 100 {
				addr := string(rune('a' + i%5))
				bannednodes.Ban(ctx, addr)
				bannednodes.IsBanned(ctx, addr)
				bannednodes.BannedAddrs(ctx)
				if r := recover(); r != nil {
					errs.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if errs.Load() != 0 {
		t.Fatalf("concurrent ops panicked %d times", errs.Load())
	}
}
