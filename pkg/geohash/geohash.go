// Package geohash 提供经纬度的 Geohash 编码/解码、邻居计算与覆盖查询,纯标准库。
//
// Geohash 把二维经纬度编码成一个 base32 字符串:前缀相同的两点在地理上相邻,
// 前缀越长精度越高。因此"附近的人/店铺/开播"可退化为**字符串前缀匹配**——
// 在 Redis/DB 里按 geohash 前缀建索引即可范围检索,无需专门的空间数据库。
//
// 与 pkg/spatial 的区别(互补):
//   - spatial 是平面网格索引(float64 x/y),适合游戏地图这类"局部平面坐标";
//   - geohash 面向真实地球经纬度(WGS84),适合 LBS——编码后可直接用字符串前缀
//     跨进程/落库检索,不必把全部实体加载进内存。
//
// 精度参考(geohash 长度 → 单元约尺寸):
//
//	4 → ~39km,5 → ~4.9km,6 → ~1.2km,7 → ~153m,8 → ~38m,9 → ~4.8m。
//
// 边界说明:Geohash 的"同前缀=相邻"在单元边界处有裂缝——两个物理相邻的点可能
// 落在不同前缀。做邻近搜索时应取中心单元 + 其 8 个邻居一起查(见 Neighbors/
// CoverNeighbors),再对候选做精确距离过滤。
//
// 纯函数,无状态,并发安全。
package geohash

import (
	"math"
	"strings"
)

// base32 是 Geohash 专用字母表(去掉了 a/i/l/o 以避免混淆)。
const base32 = "0123456789bcdefghjkmnpqrstuvwxyz"

var decodeMap [256]int8

func init() {
	for i := range decodeMap {
		decodeMap[i] = -1
	}
	for i := range len(base32) {
		decodeMap[base32[i]] = int8(i)
	}
}

// Box 是一个 Geohash 单元覆盖的经纬度范围(边界框)。
type Box struct {
	MinLat, MaxLat float64
	MinLng, MaxLng float64
}

// Center 返回边界框中心点。
func (b Box) Center() (lat, lng float64) {
	return (b.MinLat + b.MaxLat) / 2, (b.MinLng + b.MaxLng) / 2
}

// Encode 把经纬度编码为长度 precision 的 Geohash 字符串。
// precision 建议 1..12;<=0 视为 12,>12 截断为 12。
func Encode(lat, lng float64, precision int) string {
	if precision <= 0 || precision > 12 {
		precision = 12
	}
	latRange := [2]float64{-90, 90}
	lngRange := [2]float64{-180, 180}

	var sb strings.Builder
	sb.Grow(precision)
	var bit, ch int
	evenBit := true // 偶数位切经度,奇数位切纬度

	for sb.Len() < precision {
		if evenBit {
			mid := (lngRange[0] + lngRange[1]) / 2
			if lng >= mid {
				ch |= 1 << (4 - bit)
				lngRange[0] = mid
			} else {
				lngRange[1] = mid
			}
		} else {
			mid := (latRange[0] + latRange[1]) / 2
			if lat >= mid {
				ch |= 1 << (4 - bit)
				latRange[0] = mid
			} else {
				latRange[1] = mid
			}
		}
		evenBit = !evenBit

		if bit < 4 {
			bit++
		} else {
			sb.WriteByte(base32[ch])
			bit = 0
			ch = 0
		}
	}
	return sb.String()
}

// Decode 把 Geohash 字符串解码为其覆盖的边界框。
// 非法字符会被忽略(按已解析部分返回);空串返回整个世界范围。
func Decode(hash string) Box {
	latRange := [2]float64{-90, 90}
	lngRange := [2]float64{-180, 180}
	evenBit := true

	for i := 0; i < len(hash); i++ {
		cd := decodeMap[hash[i]]
		if cd < 0 {
			continue // 非法字符跳过
		}
		for bit := 4; bit >= 0; bit-- {
			b := (int(cd) >> bit) & 1
			if evenBit {
				mid := (lngRange[0] + lngRange[1]) / 2
				if b == 1 {
					lngRange[0] = mid
				} else {
					lngRange[1] = mid
				}
			} else {
				mid := (latRange[0] + latRange[1]) / 2
				if b == 1 {
					latRange[0] = mid
				} else {
					latRange[1] = mid
				}
			}
			evenBit = !evenBit
		}
	}
	return Box{MinLat: latRange[0], MaxLat: latRange[1], MinLng: lngRange[0], MaxLng: lngRange[1]}
}

// DecodeCenter 解码并返回单元中心点坐标(便捷封装)。
func DecodeCenter(hash string) (lat, lng float64) {
	return Decode(hash).Center()
}

// Neighbor 返回 hash 在指定方向上的相邻单元(同长度)。
// dLat/dLng 取 -1/0/1:如 (1,0)=北,(0,1)=东,(1,1)=东北。(0,0) 返回自身。
func Neighbor(hash string, dLat, dLng int) string {
	if hash == "" {
		return ""
	}
	box := Decode(hash)
	latHeight := box.MaxLat - box.MinLat
	lngWidth := box.MaxLng - box.MinLng
	cLat, cLng := box.Center()

	nLat := cLat + float64(dLat)*latHeight
	nLng := cLng + float64(dLng)*lngWidth
	// 经度环绕(-180..180);纬度夹取到极点内。
	nLng = wrapLng(nLng)
	nLat = math.Max(-90, math.Min(90, nLat))
	return Encode(nLat, nLng, len(hash))
}

// Neighbors 返回 hash 周围 8 个方向的邻居(不含自身),顺序:
// N, NE, E, SE, S, SW, W, NW。用于覆盖 Geohash 边界裂缝的邻近搜索。
func Neighbors(hash string) []string {
	dirs := [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	out := make([]string, 0, 8)
	for _, d := range dirs {
		out = append(out, Neighbor(hash, d[0], d[1]))
	}
	return out
}

// CoverNeighbors 返回中心单元 + 其 8 个邻居共 9 个 Geohash(去重),
// 用作邻近搜索的候选前缀集合:先按这些前缀检索,再对结果做精确距离过滤。
func CoverNeighbors(lat, lng float64, precision int) []string {
	center := Encode(lat, lng, precision)
	seen := map[string]struct{}{center: {}}
	out := []string{center}
	for _, n := range Neighbors(center) {
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	return out
}

// Distance 返回两点间的大圆距离(米),Haversine 公式。用于候选集的精确过滤/排序。
func Distance(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6371000.0 // 米
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func wrapLng(lng float64) float64 {
	for lng > 180 {
		lng -= 360
	}
	for lng < -180 {
		lng += 360
	}
	return lng
}
