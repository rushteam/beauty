package pathfind_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/pathfind"
)

func TestStraightLine(t *testing.T) {
	g := pathfind.NewGrid(5, 1)
	path := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 4, Y: 0})
	if len(path) != 5 {
		t.Fatalf("path len = %d, want 5: %v", len(path), path)
	}
	if path[0] != (pathfind.Point{X: 0, Y: 0}) || path[4] != (pathfind.Point{X: 4, Y: 0}) {
		t.Fatalf("endpoints wrong: %v", path)
	}
}

func TestSamePoint(t *testing.T) {
	g := pathfind.NewGrid(3, 3)
	p := pathfind.Point{X: 1, Y: 1}
	path := g.FindPath(p, p)
	if len(path) != 1 || path[0] != p {
		t.Fatalf("same point path = %v", path)
	}
}

func TestBlockedGoalOrStart(t *testing.T) {
	g := pathfind.NewGrid(3, 3)
	g.SetBlocked(pathfind.Point{X: 2, Y: 2}, true)
	if p := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 2, Y: 2}); p != nil {
		t.Fatalf("blocked goal should be unreachable, got %v", p)
	}
	g.SetBlocked(pathfind.Point{X: 0, Y: 0}, true)
	if p := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 1, Y: 1}); p != nil {
		t.Fatalf("blocked start should return nil, got %v", p)
	}
}

func TestOutOfBounds(t *testing.T) {
	g := pathfind.NewGrid(3, 3)
	if p := g.FindPath(pathfind.Point{X: -1, Y: 0}, pathfind.Point{X: 1, Y: 1}); p != nil {
		t.Fatal("out-of-bounds start should return nil")
	}
	if p := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 9, Y: 9}); p != nil {
		t.Fatal("out-of-bounds goal should return nil")
	}
}

func TestGoesAroundWall(t *testing.T) {
	// 5x3,中间竖墙把 (2,0)(2,1) 挡住,只能从底部 (2,2) 绕。
	g := pathfind.NewGrid(5, 3)
	g.SetBlocked(pathfind.Point{X: 2, Y: 0}, true)
	g.SetBlocked(pathfind.Point{X: 2, Y: 1}, true)

	path := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 4, Y: 0})
	if path == nil {
		t.Fatal("should find a path around the wall")
	}
	// 路径不得经过墙格。
	for _, p := range path {
		if p == (pathfind.Point{X: 2, Y: 0}) || p == (pathfind.Point{X: 2, Y: 1}) {
			t.Fatalf("path goes through wall: %v", path)
		}
	}
	// 必须经过底行绕过。
	var viaBottom bool
	for _, p := range path {
		if p == (pathfind.Point{X: 2, Y: 2}) {
			viaBottom = true
		}
	}
	if !viaBottom {
		t.Fatalf("path should detour via bottom, got %v", path)
	}
}

func TestFullyBlockedUnreachable(t *testing.T) {
	// 竖墙贯穿整列,无对角 → 不可达。
	g := pathfind.NewGrid(3, 3)
	for y := range 3 {
		g.SetBlocked(pathfind.Point{X: 1, Y: y}, true)
	}
	if p := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 2, Y: 2}); p != nil {
		t.Fatalf("should be unreachable, got %v", p)
	}
}

func TestDiagonalShorter(t *testing.T) {
	g := pathfind.NewGrid(4, 4)
	from, to := pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 3, Y: 3}

	ortho := g.FindPath(from, to)
	diag := g.FindPath(from, to, pathfind.WithDiagonal(true))
	if diag == nil || ortho == nil {
		t.Fatal("both should find paths")
	}
	// 对角允许时步数应更少(4 步对角 vs 7 步正交)。
	if len(diag) >= len(ortho) {
		t.Fatalf("diagonal path (%d) should be shorter than ortho (%d)", len(diag), len(ortho))
	}
}

func TestNoCutCorner(t *testing.T) {
	// 对角开启但禁止穿墙角:(1,0) 和 (0,1) 都堵,则 (0,0)→(1,1) 不能斜穿。
	g := pathfind.NewGrid(2, 2)
	g.SetBlocked(pathfind.Point{X: 1, Y: 0}, true)
	g.SetBlocked(pathfind.Point{X: 0, Y: 1}, true)
	path := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 1, Y: 1}, pathfind.WithDiagonal(true))
	if path != nil {
		t.Fatalf("should not cut through blocked corner, got %v", path)
	}
	// 放开穿墙角后应能斜穿。
	path = g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 1, Y: 1},
		pathfind.WithDiagonal(true), pathfind.WithCutCorner(true))
	if len(path) != 2 {
		t.Fatalf("with cut-corner should be 2 steps, got %v", path)
	}
}

func TestCostAvoidance(t *testing.T) {
	// 直线穿过高代价格,A* 应绕开走低代价路径(若绕路总代价更低)。
	g := pathfind.NewGrid(3, 3)
	// 把中间行 (1,1) 设成极高代价,起点(0,1)→终点(2,1)应绕上/下行。
	g.SetCost(pathfind.Point{X: 1, Y: 1}, 100)
	path := g.FindPath(pathfind.Point{X: 0, Y: 1}, pathfind.Point{X: 2, Y: 1})
	for _, p := range path {
		if p == (pathfind.Point{X: 1, Y: 1}) {
			t.Fatalf("should avoid high-cost cell, got %v", path)
		}
	}
}

func TestConcurrentFindPath(t *testing.T) {
	g := pathfind.NewGrid(20, 20)
	g.SetBlocked(pathfind.Point{X: 10, Y: 5}, true)
	done := make(chan bool, 10)
	for range 10 {
		go func() {
			p := g.FindPath(pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 19, Y: 19}, pathfind.WithDiagonal(true))
			done <- p != nil
		}()
	}
	for range 10 {
		if !<-done {
			t.Fatal("concurrent FindPath returned nil")
		}
	}
}
