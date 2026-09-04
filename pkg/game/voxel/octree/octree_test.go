package octree

import (
	"math"
	"testing"
)

func TestTree_AddRemove(t *testing.T) {
	tree := New[string](Box{0, 0, 0, 100, 100, 100}, 4, 8)

	tree.Add("a", 10, 20, 30)
	tree.Add("b", 50, 50, 50)
	if tree.Len() != 2 {
		t.Errorf("Len = %d, want 2", tree.Len())
	}

	x, y, z, ok := tree.Pos("a")
	if !ok || x != 10 || y != 20 || z != 30 {
		t.Errorf("Pos(a) = (%v,%v,%v,%v), want (10,20,30,true)", x, y, z, ok)
	}

	tree.Remove("a")
	if tree.Len() != 1 {
		t.Errorf("after remove, Len = %d, want 1", tree.Len())
	}
	_, _, _, ok = tree.Pos("a")
	if ok {
		t.Error("removed entity should not be found")
	}

	// 删除不存在的
	tree.Remove("nonexistent") // 不崩溃
}

func TestTree_Move(t *testing.T) {
	tree := New[string](Box{0, 0, 0, 100, 100, 100}, 4, 8)
	tree.Add("a", 10, 10, 10)
	tree.Move("a", 90, 90, 90)

	x, y, z, ok := tree.Pos("a")
	if !ok || x != 90 || y != 90 || z != 90 {
		t.Errorf("after Move: Pos = (%v,%v,%v)", x, y, z)
	}
	if tree.Len() != 1 {
		t.Errorf("Move should not change Len, got %d", tree.Len())
	}
}

func TestTree_Nearby(t *testing.T) {
	tree := New[string](Box{0, 0, 0, 100, 100, 100}, 4, 8)
	tree.Add("center", 50, 50, 50)
	tree.Add("close", 51, 50, 50)
	tree.Add("far", 90, 90, 90)

	nearby := tree.Nearby(50, 50, 50, 5)
	if len(nearby) != 2 {
		t.Errorf("Nearby len = %d, want 2", len(nearby))
	}
	// 应按距离排序
	if nearby[0].ID != "center" {
		t.Errorf("nearest should be center, got %s", nearby[0].ID)
	}
	if nearby[1].ID != "close" {
		t.Errorf("second should be close, got %s", nearby[1].ID)
	}

	// 带排除
	nearby = tree.Nearby(50, 50, 50, 5, "center")
	if len(nearby) != 1 {
		t.Errorf("Nearby with exclude len = %d, want 1", len(nearby))
	}
}

func TestTree_KNN(t *testing.T) {
	tree := New[int](Box{0, 0, 0, 100, 100, 100}, 4, 8)
	for i := 0; i < 20; i++ {
		tree.Add(i, float64(i*5), 50, 50)
	}

	knn := tree.KNN(50, 50, 50, 3, 0)
	if len(knn) != 3 {
		t.Errorf("KNN len = %d, want 3", len(knn))
	}
	// ID=10 在 (50,50,50),距离 0
	if knn[0].Dist != 0 {
		t.Errorf("KNN[0].Dist = %v, want 0", knn[0].Dist)
	}

	// 带半径限制
	knn = tree.KNN(50, 50, 50, 100, 10)
	for _, e := range knn {
		if e.Dist > 10 {
			t.Errorf("KNN with radius: dist %v > 10", e.Dist)
		}
	}
}

func TestTree_QueryBox(t *testing.T) {
	tree := New[string](Box{0, 0, 0, 100, 100, 100}, 4, 8)
	tree.Add("in", 25, 25, 25)
	tree.Add("out", 75, 75, 75)

	entities := tree.QueryBox(Box{0, 0, 0, 50, 50, 50})
	if len(entities) != 1 {
		t.Errorf("QueryBox len = %d, want 1", len(entities))
	}
	if entities[0].ID != "in" {
		t.Errorf("QueryBox entity = %s, want in", entities[0].ID)
	}
}

func TestTree_Subdivide_Collapse(t *testing.T) {
	tree := New[int](Box{0, 0, 0, 100, 100, 100}, 2, 8)

	// 添加超过 capacity,触发 subdivide
	tree.Add(1, 10, 10, 10)
	tree.Add(2, 20, 20, 20)
	tree.Add(3, 30, 30, 30)
	if tree.Len() != 3 {
		t.Errorf("after subdivide, Len = %d, want 3", tree.Len())
	}

	// 删除到 <= capacity,触发 collapse
	tree.Remove(3)
	tree.Remove(2)
	if tree.Len() != 1 {
		t.Errorf("after collapse, Len = %d, want 1", tree.Len())
	}

	// 验证剩余实体可查询
	x, y, z, ok := tree.Pos(1)
	if !ok || x != 10 || y != 10 || z != 10 {
		t.Error("remaining entity should be findable")
	}
}

func TestTree_NearbyDistance(t *testing.T) {
	tree := New[string](Box{0, 0, 0, 100, 100, 100}, 8, 8)
	tree.Add("a", 53, 50, 50)

	nearby := tree.Nearby(50, 50, 50, 10)
	if len(nearby) != 1 {
		t.Fatalf("Nearby len = %d, want 1", len(nearby))
	}
	wantDist := 3.0
	if math.Abs(nearby[0].Dist-wantDist) > 0.001 {
		t.Errorf("Dist = %v, want %v", nearby[0].Dist, wantDist)
	}
}

func BenchmarkTree_Nearby(b *testing.B) {
	tree := New[int](Box{0, 0, 0, 1000, 1000, 1000}, 16, 10)
	for i := 0; i < 10000; i++ {
		x := float64(i%100) * 10
		y := float64((i/100)%10) * 100
		z := float64(i/1000) * 100
		tree.Add(i, x, y, z)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Nearby(500, 500, 500, 50)
	}
}
