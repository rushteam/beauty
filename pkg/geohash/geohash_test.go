package geohash_test

import (
	"strings"
	"testing"

	"github.com/rushteam/beauty/pkg/geohash"
)

func TestEncode_KnownValue(t *testing.T) {
	// 经典参照:(57.64911, 10.40744) → "u4pruydqqvj"(前缀稳定)。
	got := geohash.Encode(57.64911, 10.40744, 11)
	want := "u4pruydqqvj"
	if got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
}

func TestEncode_Precision(t *testing.T) {
	full := geohash.Encode(39.9042, 116.4074, 9) // 北京
	for p := 1; p <= 9; p++ {
		got := geohash.Encode(39.9042, 116.4074, p)
		if len(got) != p {
			t.Fatalf("precision %d: len = %d", p, len(got))
		}
		// 短精度应是长精度的前缀。
		if !strings.HasPrefix(full, got) {
			t.Fatalf("precision %d: %q not prefix of %q", p, got, full)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	lat, lng := 39.9042, 116.4074
	h := geohash.Encode(lat, lng, 9)
	box := geohash.Decode(h)
	// 原点应落在解出的边界框内。
	if lat < box.MinLat || lat > box.MaxLat || lng < box.MinLng || lng > box.MaxLng {
		t.Fatalf("point not in decoded box: %+v", box)
	}
	// 中心应接近原点(精度 9 误差 < ~5m,换算成度很小)。
	clat, clng := geohash.DecodeCenter(h)
	if abs(clat-lat) > 0.001 || abs(clng-lng) > 0.001 {
		t.Fatalf("center (%v,%v) too far from (%v,%v)", clat, clng, lat, lng)
	}
}

func TestNearbyPointsSharePrefix(t *testing.T) {
	// 两个很近的点(相距几十米)应共享较长前缀。
	a := geohash.Encode(39.90420, 116.40740, 7)
	b := geohash.Encode(39.90425, 116.40745, 7)
	if a[:5] != b[:5] {
		t.Fatalf("nearby points should share 5-char prefix: %q vs %q", a, b)
	}
}

func TestFarPointsDifferentPrefix(t *testing.T) {
	beijing := geohash.Encode(39.9042, 116.4074, 6)
	shanghai := geohash.Encode(31.2304, 121.4737, 6)
	if beijing[:2] == shanghai[:2] {
		t.Fatalf("far cities should differ early: %q vs %q", beijing, shanghai)
	}
}

func TestNeighbors_CountAndDistinct(t *testing.T) {
	h := geohash.Encode(39.9042, 116.4074, 6)
	ns := geohash.Neighbors(h)
	if len(ns) != 8 {
		t.Fatalf("neighbors count = %d, want 8", len(ns))
	}
	seen := map[string]bool{h: true}
	for _, n := range ns {
		if len(n) != len(h) {
			t.Fatalf("neighbor %q wrong length", n)
		}
		if seen[n] {
			t.Fatalf("duplicate/self in neighbors: %q", n)
		}
		seen[n] = true
	}
}

func TestNeighbor_EastIsAdjacent(t *testing.T) {
	h := geohash.Encode(39.9042, 116.4074, 6)
	east := geohash.Neighbor(h, 0, 1)
	// 东邻中心经度应比原单元大。
	_, lng0 := geohash.DecodeCenter(h)
	_, lng1 := geohash.DecodeCenter(east)
	if lng1 <= lng0 {
		t.Fatalf("east neighbor lng %v should exceed %v", lng1, lng0)
	}
}

func TestCoverNeighbors(t *testing.T) {
	cover := geohash.CoverNeighbors(39.9042, 116.4074, 6)
	if len(cover) != 9 {
		t.Fatalf("cover set = %d, want 9 (center + 8)", len(cover))
	}
	// 第一个应是中心。
	if cover[0] != geohash.Encode(39.9042, 116.4074, 6) {
		t.Fatalf("cover[0] should be center")
	}
	// 全部唯一。
	seen := map[string]bool{}
	for _, c := range cover {
		if seen[c] {
			t.Fatalf("duplicate in cover: %q", c)
		}
		seen[c] = true
	}
}

func TestDistance(t *testing.T) {
	// 北京 ↔ 上海 约 1067 km。
	d := geohash.Distance(39.9042, 116.4074, 31.2304, 121.4737)
	km := d / 1000
	if km < 1000 || km > 1150 {
		t.Fatalf("Beijing-Shanghai distance = %.0f km, want ~1067", km)
	}
	// 同点距离为 0。
	if geohash.Distance(1, 2, 1, 2) != 0 {
		t.Fatal("same point distance should be 0")
	}
}

func TestEmptyAndInvalid(t *testing.T) {
	// 空串解出整个世界。
	box := geohash.Decode("")
	if box.MinLat != -90 || box.MaxLat != 90 || box.MinLng != -180 || box.MaxLng != 180 {
		t.Fatalf("empty decode = %+v, want whole world", box)
	}
	// 非法字符被忽略,不 panic。
	_ = geohash.Decode("!@#")
}

func TestPrecisionClamp(t *testing.T) {
	if len(geohash.Encode(1, 1, 0)) != 12 {
		t.Fatal("precision 0 should clamp to 12")
	}
	if len(geohash.Encode(1, 1, 100)) != 12 {
		t.Fatal("precision 100 should clamp to 12")
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
