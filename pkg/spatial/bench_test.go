package spatial_test

import (
	"math"
	"testing"

	"github.com/rushteam/beauty/pkg/spatial"
)

// side×side 均匀布点,间距 spacing,cellSize=50。均匀分布下小半径查询
// 才能体现网格"只扫近邻单元"的收益。
func uniformPoint(i, side, spacing int) (x, y float64) {
	return float64((i%side)*spacing) + 0.5, float64((i/side)*spacing) + 0.5
}

func buildIndex(side, spacing int) *spatial.Index[int] {
	ix := spatial.New[int](50)
	for i := range side * side {
		x, y := uniformPoint(i, side, spacing)
		ix.Add(i, x, y)
	}
	return ix
}

// 网格 vs 全表扫描,两种规模,查询半径固定 50(≈近邻 3×3 单元)。
// center 取平面中心。收益随 N 增大而显现(小 N 时 map 开销 ~ 抵消候选集缩减)。
func benchGrid(b *testing.B, side, spacing int) {
	ix := buildIndex(side, spacing)
	cx := float64(side * spacing / 2)
	b.ReportAllocs()
	for b.Loop() {
		_ = ix.Nearby(cx, cx, 50)
	}
}

func benchFullScan(b *testing.B, side, spacing int) {
	type pt struct {
		id   int
		x, y float64
	}
	n := side * side
	pts := make([]pt, 0, n)
	for i := range n {
		x, y := uniformPoint(i, side, spacing)
		pts = append(pts, pt{i, x, y})
	}
	cx := float64(side * spacing / 2)
	const r2 = 50.0 * 50.0
	b.ReportAllocs()
	for b.Loop() {
		var out []int
		for _, p := range pts {
			dx, dy := p.x-cx, p.y-cx
			if dx*dx+dy*dy <= r2 {
				out = append(out, p.id)
			}
		}
		_ = out
	}
}

func BenchmarkNearby_Grid_10k(b *testing.B)      { benchGrid(b, 100, 10) }
func BenchmarkNearby_FullScan_10k(b *testing.B)  { benchFullScan(b, 100, 10) }
func BenchmarkNearby_Grid_250k(b *testing.B)     { benchGrid(b, 500, 10) }
func BenchmarkNearby_FullScan_250k(b *testing.B) { benchFullScan(b, 500, 10) }

func BenchmarkMove(b *testing.B) {
	ix := buildIndex(100, 10)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		ix.Move(i%10000, math.Mod(float64(i*13), 1000), math.Mod(float64(i*29), 1000))
		i++
	}
}
