package replicate

import (
	"sync"
)

// Ack 是客户端经可靠通道确认已收到的最高帧号。
type Ack struct {
	LastFrame uint64 `json:"last_ack_frame"`
}

// CatchUpBatch 补发 (From, To] 区间内缺失的 Delta(From 不含, To 含)。
type CatchUpBatch struct {
	From   uint64  `json:"from"`
	To     uint64  `json:"to"`
	Deltas []Delta `json:"deltas"`
}

// Journal 保存最近 depth 帧的 Delta(按 viewer 独立),供丢包后 CatchUp。
type Journal struct {
	mu      sync.RWMutex
	depth   int
	entries []Delta
}

// NewJournal 创建 Journal。depth<=0 时取 128。
func NewJournal(depth int) *Journal {
	if depth <= 0 {
		depth = 128
	}
	return &Journal{depth: depth}
}

// Record 追加一帧 Delta;超出 depth 时丢弃最旧帧。
func (j *Journal) Record(d Delta) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, d)
	if len(j.entries) > j.depth {
		j.entries = j.entries[len(j.entries)-j.depth:]
	}
}

// CatchUp 返回 frame ∈ (after, through] 的 Delta 列表。
func (j *Journal) CatchUp(after, through uint64) CatchUpBatch {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := CatchUpBatch{From: after, To: through}
	for _, d := range j.entries {
		if d.Frame > after && d.Frame <= through {
			out.Deltas = append(out.Deltas, d)
		}
	}
	return out
}

// Latest 返回 journal 中最高帧号(无条目时为 0)。
func (j *Journal) Latest() uint64 {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if len(j.entries) == 0 {
		return 0
	}
	return j.entries[len(j.entries)-1].Frame
}

// ViewerTrack 跟踪单个连接的发送/确认进度,并生成 CatchUp。
type ViewerTrack struct {
	mu       sync.Mutex
	journal  *Journal
	lastAck  uint64
	lastSent uint64
}

// NewViewerTrack 创建 viewer 追踪器。
func NewViewerTrack(journal *Journal) *ViewerTrack {
	if journal == nil {
		journal = NewJournal(0)
	}
	return &ViewerTrack{journal: journal}
}

// RecordSent 在不可靠通道发送 delta 后调用。
func (v *ViewerTrack) RecordSent(d Delta) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.journal.Record(d)
	if d.Frame > v.lastSent {
		v.lastSent = d.Frame
	}
}

// OnAck 处理客户端 Ack;若存在缺口则返回需补发的 CatchUpBatch。
func (v *ViewerTrack) OnAck(ack Ack) CatchUpBatch {
	v.mu.Lock()
	defer v.mu.Unlock()
	if ack.LastFrame > v.lastAck {
		v.lastAck = ack.LastFrame
	}
	if v.lastAck >= v.lastSent {
		return CatchUpBatch{From: v.lastAck, To: v.lastSent}
	}
	return v.journal.CatchUp(v.lastAck, v.lastSent)
}

// Gap 返回 lastSent - lastAck(未确认帧数)。
func (v *ViewerTrack) Gap() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.lastSent <= v.lastAck {
		return 0
	}
	return v.lastSent - v.lastAck
}

// LastAck 返回已确认帧号。
func (v *ViewerTrack) LastAck() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastAck
}
