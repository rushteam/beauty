package sketch

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"
)

func TestTDigestJSONRoundTrip(t *testing.T) {
	td := NewTDigest(100)
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 50000; i++ {
		td.Add(rng.Float64() * 100)
	}
	p99 := td.Quantile(0.99)

	wire, err := json.Marshal(td)
	if err != nil {
		t.Fatal(err)
	}
	var got TDigest
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if got.Count() != td.Count() {
		t.Fatalf("count %v != %v", got.Count(), td.Count())
	}
	if math.Abs(got.Quantile(0.99)-p99) > 1e-9 {
		t.Fatalf("p99 after roundtrip: %v != %v", got.Quantile(0.99), p99)
	}
}

// 跨进程汇总:两个实例各自统计,序列化上报,汇总端合并得全局分位数。
func TestTDigestDistributedAggregate(t *testing.T) {
	n1 := NewTDigest(100)
	n2 := NewTDigest(100)
	all := NewTDigest(100)
	rng := rand.New(rand.NewSource(6))
	for i := 0; i < 30000; i++ {
		x := rng.NormFloat64()*5 + 50
		n1.Add(x)
		all.Add(x)
		y := rng.NormFloat64()*5 + 50
		n2.Add(y)
		all.Add(y)
	}

	// n1、n2 序列化上报到汇总端。
	agg := NewTDigest(100)
	for _, nd := range []*TDigest{n1, n2} {
		wire, _ := json.Marshal(nd)
		recv := NewTDigest(100)
		if err := json.Unmarshal(wire, recv); err != nil {
			t.Fatal(err)
		}
		agg.Merge(recv)
	}

	if agg.Count() != 60000 {
		t.Fatalf("agg count=%v want 60000", agg.Count())
	}
	// 汇总结果应接近把所有数据直接喂进一个 digest。
	if math.Abs(agg.Quantile(0.5)-all.Quantile(0.5)) > 1.0 {
		t.Fatalf("aggregated p50 %v vs reference %v", agg.Quantile(0.5), all.Quantile(0.5))
	}
}
