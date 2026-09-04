package octree

import (
	"sync"
	"testing"
)

// TestTree_ConcurrentOps 并发增删改查,验证 -race 安全。
func TestTree_ConcurrentOps(t *testing.T) {
	tree := New[int](Box{X: 0, Y: 0, Z: 0, W: 1000, H: 1000, D: 1000}, 8, 10)
	var wg sync.WaitGroup
	const goroutines = 8
	const ops = 500

	// 并发写
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				eid := id*ops + i
				x := float64(eid%100) * 10
				y := float64((eid/100)%10) * 100
				z := float64(eid/1000) * 100
				switch i % 4 {
				case 0:
					tree.Add(eid, x, y, z)
				case 1:
					tree.Move(eid, x+1, y+1, z+1)
				case 2:
					tree.Remove(eid)
				case 3:
					tree.Add(eid, x, y, z)
				}
			}
		}(g)
	}

	// 并发读
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				tree.Nearby(500, 500, 500, 100)
				tree.KNN(500, 500, 500, 5, 200)
				tree.QueryBox(Box{X: 400, Y: 400, Z: 400, W: 200, H: 200, D: 200})
				tree.Pos(id*ops + i)
				tree.Len()
			}
		}(g)
	}

	wg.Wait()
}
