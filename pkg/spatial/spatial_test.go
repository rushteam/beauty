package spatial_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/spatial"
)

func TestAddAndNearby(t *testing.T) {
	ix := spatial.New[string](10)
	ix.Add("a", 0, 0)
	ix.Add("b", 3, 4)   // 距原点 5
	ix.Add("c", 30, 30) // 远

	got := ix.Nearby(0, 0, 10)
	if len(got) != 2 {
		t.Fatalf("nearby count = %d, want 2: %v", len(got), got)
	}
	// 按距离升序:a(0) 在 b(5) 前。
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("order wrong: %v", got)
	}
	if got[1].Dist < 4.9 || got[1].Dist > 5.1 {
		t.Fatalf("dist b = %v, want 5", got[1].Dist)
	}
}

func TestNearby_RadiusBoundary(t *testing.T) {
	ix := spatial.New[string](5)
	ix.Add("edge", 10, 0) // 距原点正好 10
	if got := ix.Nearby(0, 0, 10); len(got) != 1 {
		t.Fatalf("boundary should be included, got %d", len(got))
	}
	if got := ix.Nearby(0, 0, 9.9); len(got) != 0 {
		t.Fatalf("just outside should be excluded, got %d", len(got))
	}
}

func TestExcludeSelf(t *testing.T) {
	ix := spatial.New[string](10)
	ix.Add("me", 0, 0)
	ix.Add("you", 1, 1)
	got := ix.Nearby(0, 0, 10, "me")
	if len(got) != 1 || got[0].ID != "you" {
		t.Fatalf("exclude self failed: %v", got)
	}
}

func TestMove(t *testing.T) {
	ix := spatial.New[string](10)
	ix.Add("a", 0, 0)
	// 移到远处后,原点附近查不到。
	ix.Move("a", 100, 100)
	if got := ix.Nearby(0, 0, 10); len(got) != 0 {
		t.Fatalf("after move away, nearby = %v", got)
	}
	if got := ix.Nearby(100, 100, 10); len(got) != 1 {
		t.Fatalf("should find at new pos, got %v", got)
	}
	x, y, ok := ix.Pos("a")
	if !ok || x != 100 || y != 100 {
		t.Fatalf("pos = (%v,%v,%v)", x, y, ok)
	}
}

func TestRemove(t *testing.T) {
	ix := spatial.New[string](10)
	ix.Add("a", 1, 1)
	ix.Remove("a")
	if ix.Len() != 0 {
		t.Fatalf("len after remove = %d", ix.Len())
	}
	if _, _, ok := ix.Pos("a"); ok {
		t.Fatal("removed entity should not have pos")
	}
	if got := ix.Nearby(0, 0, 100); len(got) != 0 {
		t.Fatalf("removed entity should not appear: %v", got)
	}
}

func TestKNN(t *testing.T) {
	ix := spatial.New[int](10)
	// 沿 x 轴放 10 个点:距原点 1,2,...,10
	for i := 1; i <= 10; i++ {
		ix.Add(i, float64(i), 0)
	}
	got := ix.KNN(0, 0, 3, 100)
	if len(got) != 3 {
		t.Fatalf("knn count = %d, want 3", len(got))
	}
	// 最近 3 个应是 1,2,3
	for i, e := range got {
		if e.ID != i+1 {
			t.Fatalf("knn[%d] = %d, want %d", i, e.ID, i+1)
		}
	}
}

func TestKNN_FewerThanK(t *testing.T) {
	ix := spatial.New[int](10)
	ix.Add(1, 1, 1)
	ix.Add(2, 2, 2)
	got := ix.KNN(0, 0, 5, 100)
	if len(got) != 2 {
		t.Fatalf("knn with only 2 entities = %d, want 2", len(got))
	}
}

func TestManyCellsAcrossGrid(t *testing.T) {
	// 实体散布在多个网格单元,验证跨单元查询正确。
	ix := spatial.New[string](10)
	ix.Add("near1", 5, 5)
	ix.Add("near2", 12, 8) // 相邻单元,距(5,5)约 7.6
	ix.Add("far", 90, 90)
	got := ix.Nearby(5, 5, 10)
	ids := map[string]bool{}
	for _, e := range got {
		ids[e.ID] = true
	}
	if !ids["near1"] || !ids["near2"] || ids["far"] {
		t.Fatalf("cross-cell query wrong: %v", got)
	}
}

func TestConcurrent(t *testing.T) {
	ix := spatial.New[int](10)
	var wg sync.WaitGroup
	// 并发写
	for i := range 500 {
		wg.Go(func() {
			ix.Add(i, float64(i%50), float64(i/50))
		})
	}
	wg.Wait()
	// 并发读
	for range 50 {
		wg.Go(func() {
			_ = ix.Nearby(25, 5, 15)
			_ = ix.KNN(25, 5, 10, 30)
		})
	}
	wg.Wait()
	if ix.Len() != 500 {
		t.Fatalf("len = %d, want 500", ix.Len())
	}
}

func TestReAddUpdatesPosition(t *testing.T) {
	ix := spatial.New[string](10)
	ix.Add("a", 0, 0)
	ix.Add("a", 50, 50) // 同 ID 再 Add = 更新
	if ix.Len() != 1 {
		t.Fatalf("re-add should not duplicate, len = %d", ix.Len())
	}
	if got := ix.Nearby(50, 50, 5); len(got) != 1 {
		t.Fatalf("re-add should update pos: %v", got)
	}
}

func Example() {
	ix := spatial.New[string](100)
	ix.Add("alice", 10, 10)
	ix.Add("bob", 20, 15)
	ix.Add("carol", 500, 500)

	for _, e := range ix.Nearby(0, 0, 50, "alice") {
		fmt.Printf("%s at (%.0f,%.0f) dist=%.1f\n", e.ID, e.X, e.Y, e.Dist)
	}
	// Output:
	// bob at (20,15) dist=25.0
}
