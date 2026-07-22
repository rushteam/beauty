package sketch_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/rushteam/beauty/pkg/sketch"
)

func TestHyperLogLog_Estimate(t *testing.T) {
	h := sketch.NewHyperLogLog(14)
	const n = 100000
	for i := 0; i < n; i++ {
		h.AddString(fmt.Sprintf("user-%d", i))
	}
	est := h.Count()
	errRate := math.Abs(float64(est)-n) / n
	if errRate > 0.03 {
		t.Fatalf("基数估计误差过大: est=%d 真值=%d 误差=%.3f", est, n, errRate)
	}
}

func TestHyperLogLog_Dedup(t *testing.T) {
	h := sketch.NewHyperLogLog(14)
	for i := 0; i < 10000; i++ {
		h.AddString("same") // 重复元素不应增加基数
	}
	if est := h.Count(); est > 3 {
		t.Fatalf("重复元素基数应约为 1, got %d", est)
	}
}

func TestHyperLogLog_Merge(t *testing.T) {
	a := sketch.NewHyperLogLog(14)
	b := sketch.NewHyperLogLog(14)
	for i := 0; i < 50000; i++ {
		a.AddString(fmt.Sprintf("a-%d", i))
	}
	for i := 0; i < 50000; i++ {
		b.AddString(fmt.Sprintf("b-%d", i))
	}
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	est := a.Count()
	errRate := math.Abs(float64(est)-100000) / 100000
	if errRate > 0.03 {
		t.Fatalf("Merge 后基数误差过大: est=%d 误差=%.3f", est, errRate)
	}
	// precision 不一致应报错。
	if a.Merge(sketch.NewHyperLogLog(10)) == nil {
		t.Fatal("precision 不一致应 Merge 失败")
	}
}

func TestCountMin_Frequency(t *testing.T) {
	c := sketch.NewCountMin(2048, 5)
	for i := 0; i < 1000; i++ {
		c.Add("hot", 1)
	}
	c.Add("cold", 1)
	// Count-Min 只会高估:hot 估计 >= 真实 1000。
	if got := c.Count("hot"); got < 1000 {
		t.Fatalf("hot 频率应 >= 1000, got %d", got)
	}
	// cold 估计应接近 1(可能因碰撞略高,给宽松上界)。
	if got := c.Count("cold"); got < 1 || got > 20 {
		t.Fatalf("cold 频率应约为 1, got %d", got)
	}
	// 未出现的 key 估计应很小。
	if got := c.Count("never"); got > 20 {
		t.Fatalf("未出现 key 估计应很小, got %d", got)
	}
}

func TestCountMin_WithError(t *testing.T) {
	c := sketch.NewCountMinWithError(0.001, 0.01)
	c.Add("x", 42)
	if got := c.Count("x"); got < 42 {
		t.Fatalf("应 >= 42, got %d", got)
	}
}

func TestReservoir_Basic(t *testing.T) {
	r := sketch.NewReservoir[int](100)
	for i := 0; i < 10000; i++ {
		r.Add(i)
	}
	if r.Len() != 100 {
		t.Fatalf("样本数应为 k=100, got %d", r.Len())
	}
	if r.Count() != 10000 {
		t.Fatalf("已见总数应为 10000, got %d", r.Count())
	}
	// 所有样本都应来自流(0..9999)且不重复。
	seen := map[int]bool{}
	for _, v := range r.Sample() {
		if v < 0 || v >= 10000 {
			t.Fatalf("样本越界: %d", v)
		}
		if seen[v] {
			t.Fatalf("样本重复: %d", v)
		}
		seen[v] = true
	}
}

// 不足 k 时应全部保留。
func TestReservoir_UnderCapacity(t *testing.T) {
	r := sketch.NewReservoir[string](10)
	r.Add("a")
	r.Add("b")
	if r.Len() != 2 || r.Count() != 2 {
		t.Fatalf("Len=%d Count=%d", r.Len(), r.Count())
	}
}

// 采样大致均匀:统计每个元素入选频率,应接近 k/n。
func TestReservoir_Uniformity(t *testing.T) {
	const n, k, trials = 20, 5, 20000
	counts := make([]int, n)
	for tr := 0; tr < trials; tr++ {
		r := sketch.NewReservoir[int](k)
		for i := 0; i < n; i++ {
			r.Add(i)
		}
		for _, v := range r.Sample() {
			counts[v]++
		}
	}
	want := float64(trials) * k / n // 每个元素期望入选次数
	for i, c := range counts {
		dev := math.Abs(float64(c)-want) / want
		if dev > 0.15 {
			t.Fatalf("元素 %d 入选偏差过大: got %d want ~%.0f (dev %.2f)", i, c, want, dev)
		}
	}
}
