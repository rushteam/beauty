// geohash 示例:经纬度地理编码 + 附近检索。
//
// 演示 pkg/geohash:编码/解码、前缀相邻性、覆盖邻居(附近的人)、大圆距离。
// 场景:LBS「附近的人/店铺」——按 geohash 前缀在 DB/Redis 检索,再精确过滤。
package main

import (
	"fmt"

	"github.com/rushteam/beauty/pkg/geohash"
)

func main() {
	// 天安门附近。
	lat, lng := 39.9042, 116.4074
	h := geohash.Encode(lat, lng, 8)
	fmt.Printf("(%.4f, %.4f) → geohash: %s\n", lat, lng, h)

	// 不同精度是前缀关系。
	fmt.Println("\n不同精度(前缀递进):")
	for p := 4; p <= 8; p++ {
		fmt.Printf("  精度 %d: %s\n", p, geohash.Encode(lat, lng, p))
	}

	// 附近检索:中心 + 8 邻居的前缀集合(覆盖边界裂缝)。
	fmt.Println("\n附近检索用的覆盖前缀集(精度 6):")
	for _, c := range geohash.CoverNeighbors(lat, lng, 6) {
		fmt.Printf("  %s\n", c)
	}

	// 用 geohash 前缀粗筛后,用大圆距离精确过滤/排序。
	type poi struct {
		name     string
		lat, lng float64
	}
	pois := []poi{
		{"故宫", 39.9163, 116.3972},
		{"王府井", 39.9097, 116.4180},
		{"上海外滩", 31.2397, 121.4900},
	}
	fmt.Println("\n各 POI 到天安门的距离:")
	for _, p := range pois {
		d := geohash.Distance(lat, lng, p.lat, p.lng)
		fmt.Printf("  %-8s %.2f km\n", p.name, d/1000)
	}
}
