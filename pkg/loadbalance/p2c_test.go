package loadbalance_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/loadbalance"
)

func TestP2C_Empty(t *testing.T) {
	lb := loadbalance.NewP2C([]testNode{})
	if _, _, ok := lb.Pick(); ok {
		t.Fatal("空节点应返回 ok=false")
	}
}

func TestP2C_Single(t *testing.T) {
	lb := loadbalance.NewP2C([]testNode{{id: "a", weight: 1}})
	for i := 0; i < 10; i++ {
		n, done, ok := lb.Pick()
		if !ok || n.id != "a" || done == nil {
			t.Fatalf("单节点应恒选 a, got %+v ok=%v", n, ok)
		}
		done(nil)
	}
}

// 核心特性:一个节点持续慢,流量应自动偏向快节点。
func TestP2C_FavorsFaster(t *testing.T) {
	lb := loadbalance.NewP2C([]testNode{{id: "slow", weight: 1}, {id: "fast", weight: 1}})
	counts := map[string]int{}
	for i := 0; i < 200; i++ {
		n, done, ok := lb.Pick()
		if !ok {
			t.Fatal("应有可用节点")
		}
		counts[n.id]++
		if n.id == "slow" {
			time.Sleep(time.Millisecond) // 慢节点:高延迟
		}
		done(nil)
	}
	if counts["fast"] <= counts["slow"] {
		t.Fatalf("应偏向快节点: fast=%d slow=%d", counts["fast"], counts["slow"])
	}
}

// 失败会拉低健康度,同样应减少被选中。
func TestP2C_AvoidsErroring(t *testing.T) {
	lb := loadbalance.NewP2C([]testNode{{id: "bad", weight: 1}, {id: "good", weight: 1}})
	errBad := errors.New("bad")
	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		n, done, ok := lb.Pick()
		if !ok {
			t.Fatal("应有可用节点")
		}
		counts[n.id]++
		if n.id == "bad" {
			time.Sleep(time.Millisecond)
			done(errBad)
		} else {
			done(nil)
		}
	}
	if counts["good"] <= counts["bad"] {
		t.Fatalf("应偏向健康节点: good=%d bad=%d", counts["good"], counts["bad"])
	}
}

func TestP2C_Update(t *testing.T) {
	lb := loadbalance.NewP2C([]testNode{{id: "a", weight: 1}, {id: "b", weight: 1}})
	lb.Update([]testNode{{id: "a", weight: 1}}) // 缩到 1 个
	if len(lb.Nodes()) != 1 {
		t.Fatalf("Update 后应剩 1 个节点, got %d", len(lb.Nodes()))
	}
	n, done, ok := lb.Pick()
	if !ok || n.id != "a" {
		t.Fatalf("应只选到 a, got %+v", n)
	}
	done(nil)
	lb.Update(nil) // 清空
	if _, _, ok := lb.Pick(); ok {
		t.Fatal("清空后应 ok=false")
	}
}

// 并发 Pick/done 无数据竞争(-race)。
func TestP2C_Concurrent(t *testing.T) {
	lb := loadbalance.NewP2C([]testNode{{id: "a", weight: 1}, {id: "b", weight: 1}, {id: "c", weight: 1}})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				n, done, ok := lb.Pick()
				if !ok {
					t.Error("应有可用节点")
					return
				}
				_ = n
				done(nil)
			}
		}()
	}
	wg.Wait()
}
