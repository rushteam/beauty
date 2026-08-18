package matchmaker

import "github.com/rushteam/beauty/pkg/geohash"

const (
	// AttrSkill 是默认技能分 numeric 属性名。
	AttrSkill = "skill"
	// AttrLatency 是延迟(毫秒) numeric 属性名。
	AttrLatency = "latency"
	// AttrLat / AttrLng 是地理匹配用的经纬度 numeric 属性名。
	AttrLat = "lat"
	AttrLng = "lng"

	attrGeo = "geo" // GeoScore 哨兵,不作为 ticket 属性名
)

// MatchFunc 评估两个 ticket 的匹配质量,返回 [0,1](1=完美匹配,<=0 视为不兼容)。
type MatchFunc func(a, b *Ticket) float64

// Dimension 一个 numeric 属性的匹配维度。
type Dimension struct {
	Attr     string  // numeric 属性名("skill","latency")或 GeoScore 返回的哨兵
	Weight   float64 // 权重,MultiDimScore 会按总和归一化
	MaxDelta float64 // 最大容忍差值,超过则该维得 0;<=0 时跳过该维
}

// SkillScore 按 skill 差值评分。maxDelta 为最大可接受分差。
func SkillScore(maxDelta, weight float64) Dimension {
	return Dimension{Attr: AttrSkill, Weight: weight, MaxDelta: maxDelta}
}

// LatencyScore 按延迟差值评分。maxDeltaMs 为最大可接受延迟差(毫秒)。
func LatencyScore(maxDeltaMs, weight float64) Dimension {
	return Dimension{Attr: AttrLatency, Weight: weight, MaxDelta: maxDeltaMs}
}

// GeoScore 按 Haversine 距离评分。maxKm 为最大可接受距离(千米),
// ticket 需带 lat/lng numeric 属性。
func GeoScore(maxKm, weight float64) Dimension {
	return Dimension{Attr: attrGeo, Weight: weight, MaxDelta: maxKm}
}

// MultiDimScore 创建多维匹配评分函数。各维加权平均,任一维得 0 则整体为 0
// (硬约束:超出 MaxDelta 即不匹配)。weights 会被归一化;全为 0 时返回恒 0。
func MultiDimScore(dims ...Dimension) MatchFunc {
	var totalW float64
	active := make([]Dimension, 0, len(dims))
	for _, d := range dims {
		if d.MaxDelta <= 0 || d.Weight <= 0 {
			continue
		}
		active = append(active, d)
		totalW += d.Weight
	}
	if totalW <= 0 {
		return func(a, b *Ticket) float64 { return 0 }
	}
	return func(a, b *Ticket) float64 {
		if a == nil || b == nil {
			return 0
		}
		var sum float64
		for _, d := range active {
			s := dimScore(d, a, b)
			if s <= 0 {
				return 0
			}
			sum += d.Weight * s
		}
		return sum / totalW
	}
}

func dimScore(d Dimension, a, b *Ticket) float64 {
	if d.Attr == attrGeo {
		return geoDimScore(d.MaxDelta, a, b)
	}
	av, aok := numeric(a, d.Attr)
	bv, bok := numeric(b, d.Attr)
	if !aok || !bok {
		return 0
	}
	delta := av - bv
	if delta < 0 {
		delta = -delta
	}
	if delta >= d.MaxDelta {
		return 0
	}
	return 1 - delta/d.MaxDelta
}

func geoDimScore(maxKm float64, a, b *Ticket) float64 {
	alat, aok1 := numeric(a, AttrLat)
	alng, aok2 := numeric(a, AttrLng)
	blat, bok1 := numeric(b, AttrLat)
	blng, bok2 := numeric(b, AttrLng)
	if !aok1 || !aok2 || !bok1 || !bok2 {
		return 0
	}
	km := geohash.Distance(alat, alng, blat, blng) / 1000
	if km >= maxKm {
		return 0
	}
	return 1 - km/maxKm
}

func numeric(t *Ticket, attr string) (float64, bool) {
	if t == nil || t.Properties.Numeric == nil {
		return 0, false
	}
	v, ok := t.Properties.Numeric[attr]
	return v, ok
}
