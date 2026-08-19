package matchmaker

import (
	"math"
	"testing"
)

func TestMultiDimScore_SkillAndLatency(t *testing.T) {
	fn := MultiDimScore(
		SkillScore(200, 0.7),
		LatencyScore(100, 0.3),
	)
	a := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrSkill: 1000, AttrLatency: 40,
	}}}
	close := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrSkill: 1050, AttrLatency: 50,
	}}}
	farSkill := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrSkill: 1500, AttrLatency: 40,
	}}}
	farPing := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrSkill: 1000, AttrLatency: 200,
	}}}

	sClose := fn(a, close)
	if sClose <= 0 || sClose > 1 {
		t.Fatalf("close score=%v want (0,1]", sClose)
	}
	if fn(a, farSkill) != 0 {
		t.Fatal("skill delta over max should be 0")
	}
	if fn(a, farPing) != 0 {
		t.Fatal("latency delta over max should be 0")
	}
}

func TestGeoScore_NearbyVsFar(t *testing.T) {
	fn := MultiDimScore(GeoScore(50, 1))
	beijing := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrLat: 39.90, AttrLng: 116.40,
	}}}
	nearby := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrLat: 39.91, AttrLng: 116.41,
	}}}
	shanghai := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrLat: 31.23, AttrLng: 121.47,
	}}}
	sNear := fn(beijing, nearby)
	if sNear <= 0 {
		t.Fatalf("nearby geo score=%v want >0", sNear)
	}
	if fn(beijing, shanghai) != 0 {
		t.Fatal("shanghai should exceed 50km")
	}
}

func TestMultiDimScore_MissingAttr(t *testing.T) {
	fn := MultiDimScore(SkillScore(100, 1))
	a := &Ticket{Properties: Properties{Numeric: map[string]float64{AttrSkill: 1000}}}
	b := &Ticket{Properties: Properties{Numeric: map[string]float64{}}}
	if fn(a, b) != 0 {
		t.Fatal("missing attr should score 0")
	}
}

func TestMultiDimScore_EmptyDims(t *testing.T) {
	fn := MultiDimScore()
	a := &Ticket{Properties: Properties{Numeric: map[string]float64{AttrSkill: 1}}}
	if fn(a, a) != 0 {
		t.Fatal("empty dims should score 0")
	}
}

func TestDimScore_Identical(t *testing.T) {
	fn := MultiDimScore(SkillScore(100, 1), LatencyScore(50, 1))
	a := &Ticket{Properties: Properties{Numeric: map[string]float64{
		AttrSkill: 1200, AttrLatency: 30,
	}}}
	got := fn(a, a)
	if math.Abs(got-1) > 1e-9 {
		t.Fatalf("identical tickets score=%v want 1", got)
	}
}
