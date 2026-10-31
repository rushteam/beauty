package sketch

import (
	"encoding/json"
	"math"
)

// 本文件给 TDigest 加 JSON 序列化,用于跨进程/跨实例汇总分位数:各实例本地统计,把 digest
// 序列化上报,汇总端 Unmarshal 后用 Merge 合并,即可得全局近似分位数(如全集群 p99)。
//
// 序列化前会先把未归并缓冲 flush 进质心,保证传输的是完整压缩状态。

type tdJSON struct {
	Compression float64   `json:"compression"`
	Means       []float64 `json:"means"`
	Weights     []float64 `json:"weights"`
	Count       float64   `json:"count"`
	Min         float64   `json:"min"`
	Max         float64   `json:"max"`
}

// MarshalJSON 实现 json.Marshaler(会先归并缓冲)。
func (t *TDigest) MarshalJSON() ([]byte, error) {
	t.mu.Lock()
	t.process()
	w := tdJSON{
		Compression: t.compression,
		Means:       append([]float64(nil), t.means...),
		Weights:     append([]float64(nil), t.weights...),
		Count:       t.count,
		Min:         t.min,
		Max:         t.max,
	}
	t.mu.Unlock()
	return json.Marshal(w)
}

// UnmarshalJSON 实现 json.Unmarshaler,用序列化状态重建 digest(覆盖原有内容)。
func (t *TDigest) UnmarshalJSON(b []byte) error {
	var w tdJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if w.Compression <= 0 {
		w.Compression = 100
	}
	t.compression = w.Compression
	t.means = w.Means
	t.weights = w.Weights
	t.count = w.Count
	if w.Count == 0 {
		t.min, t.max = math.Inf(1), math.Inf(-1)
	} else {
		t.min, t.max = w.Min, w.Max
	}
	t.tmpMeans, t.tmpWeights, t.tmpCount = nil, nil, 0
	return nil
}
