// Package interpolate 提供客户端渲染侧的状态插值与抖动缓冲。
//
// 解决的问题:服务器以固定 tick 下发 Delta(如 20Hz/50ms),但客户端渲染跑 60fps。
// 如果直接"收到就显示",表现为:网络好时 50ms 一跳,网络抖时更不规则。
// 玩家感知为"卡顿"或"瞬移"。
//
// 本包的方案:客户端始终渲染"过去 T 毫秒"的世界状态,在两个已知快照之间做
// 平滑插值。T 称为"渲染延迟"(render delay / interpolation window),通常
// 取 2~3 个 tick 间隔(如 100~150ms)。代价是多了固定延迟,但换来丝滑。
//
// 三大组件:
//   - Buffer: 接收服务器快照,按服务器时间戳排序存储;
//   - Interpolator: 给定"渲染时间",从 Buffer 中找前后两帧做线性插值;
//   - TimeLine: 客户端本地时钟 → 服务器时间轴的映射(含抖动吸收)。
//
// 并发:Buffer 并发安全(网络线程写、渲染线程读);Interpolator/TimeLine 为值或
// 单线程使用(渲染循环内)。
package interpolate

import (
	"sync"
	"time"
)

// ---------- Snapshot ----------

// Snapshot 表示一个可插值的实体状态快照。
type Snapshot struct {
	ID      string
	X, Y    float64
	VX, VY  float64 // 可选:速度(用于 Hermite 插值或外推)
	Angle   float64 // 可选:朝向角度
	Payload any     // 业务扩展字段(不参与插值)
}

// ---------- Frame ----------

// Frame 是一帧服务器下发的世界快照(含时间戳)。
type Frame struct {
	ServerTime time.Duration // 服务器时间轴上的时间戳
	Entities   []Snapshot
}

// ---------- Buffer ----------

// Buffer 是带抖动吸收的快照缓冲。网络线程 Push,渲染线程 Sample。
// 并发安全。
type Buffer struct {
	mu     sync.RWMutex
	frames []Frame
	cap    int
}

// NewBuffer 创建缓冲区。capacity 为保留的最大帧数(推荐 32~64)。
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 32
	}
	return &Buffer{
		frames: make([]Frame, 0, capacity),
		cap:    capacity,
	}
}

// Push 插入一帧服务器快照。帧按 ServerTime 单调递增;乱序帧被插入到正确位置。
func (b *Buffer) Push(f Frame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// 快速路径:大多数时候是追加
	if len(b.frames) == 0 || f.ServerTime >= b.frames[len(b.frames)-1].ServerTime {
		b.frames = append(b.frames, f)
	} else {
		// 乱序:二分插入
		pos := b.searchInsertPos(f.ServerTime)
		b.frames = append(b.frames, Frame{})
		copy(b.frames[pos+1:], b.frames[pos:])
		b.frames[pos] = f
	}
	// 淘汰旧帧
	if len(b.frames) > b.cap {
		excess := len(b.frames) - b.cap
		copy(b.frames, b.frames[excess:])
		b.frames = b.frames[:b.cap]
	}
}

func (b *Buffer) searchInsertPos(t time.Duration) int {
	lo, hi := 0, len(b.frames)
	for lo < hi {
		mid := (lo + hi) / 2
		if b.frames[mid].ServerTime < t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Bracket 返回 renderTime 前后两帧及插值因子 t∈[0,1]。
// 若缓冲不足(尚无两帧跨 renderTime),返回 ok=false。
func (b *Buffer) Bracket(renderTime time.Duration) (before, after Frame, t float64, ok bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.frames)
	if n < 2 {
		return Frame{}, Frame{}, 0, false
	}
	// 找到第一个 ServerTime > renderTime 的帧
	idx := -1
	for i := range n {
		if b.frames[i].ServerTime > renderTime {
			idx = i
			break
		}
	}
	if idx <= 0 {
		// renderTime 在所有帧之前或恰好最后一帧之后
		if n >= 2 && renderTime >= b.frames[n-1].ServerTime {
			// 超前:用最后两帧外推
			before = b.frames[n-2]
			after = b.frames[n-1]
			span := after.ServerTime - before.ServerTime
			if span <= 0 {
				return before, after, 1, true
			}
			t = float64(renderTime-before.ServerTime) / float64(span)
			return before, after, t, true
		}
		return Frame{}, Frame{}, 0, false
	}
	before = b.frames[idx-1]
	after = b.frames[idx]
	span := after.ServerTime - before.ServerTime
	if span <= 0 {
		return before, after, 0, true
	}
	t = float64(renderTime-before.ServerTime) / float64(span)
	return before, after, t, true
}

// Latest 返回缓冲中最新帧的服务器时间。无帧时返回 0。
func (b *Buffer) Latest() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.frames) == 0 {
		return 0
	}
	return b.frames[len(b.frames)-1].ServerTime
}

// Len 返回缓冲帧数。
func (b *Buffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.frames)
}

// ---------- TimeLine ----------

// TimeLine 维护客户端本地时钟到服务器时间轴的映射。
// 核心思路:服务器每个包附带 serverTime,客户端收到后校准偏移量。
// 渲染时间 = 本地时间 - 偏移量 - renderDelay。
type TimeLine struct {
	offset      time.Duration // localTime - serverTime(EMA 平滑)
	renderDelay time.Duration
	samples     int
	now         func() time.Time
	epoch       time.Time
}

// TimeLineOption 配置 TimeLine。
type TimeLineOption func(*TimeLine)

// WithRenderDelay 设置渲染延迟(默认 100ms)。
func WithRenderDelay(d time.Duration) TimeLineOption {
	return func(tl *TimeLine) { tl.renderDelay = d }
}

// WithNow 注入时钟(测试用)。
func WithNow(fn func() time.Time) TimeLineOption {
	return func(tl *TimeLine) {
		if fn != nil {
			tl.now = fn
		}
	}
}

// NewTimeLine 创建时间轴。epoch 为本地起始时间(通常 time.Now())。
func NewTimeLine(opts ...TimeLineOption) *TimeLine {
	tl := &TimeLine{
		renderDelay: 100 * time.Millisecond,
		now:         time.Now,
	}
	for _, o := range opts {
		o(tl)
	}
	tl.epoch = tl.now()
	return tl
}

// OnServerFrame 收到服务器帧时调用:用本地收到时间和帧的服务器时间校准偏移。
func (tl *TimeLine) OnServerFrame(serverTime time.Duration) {
	localElapsed := tl.now().Sub(tl.epoch)
	newOffset := localElapsed - serverTime
	tl.samples++
	if tl.samples == 1 {
		tl.offset = newOffset
		return
	}
	// EMA(α = 0.1):缓慢追踪,吸收抖动
	const alpha = 0.1
	tl.offset = time.Duration(float64(tl.offset)*(1-alpha) + float64(newOffset)*alpha)
}

// RenderTime 返回当前应渲染的服务器时间(= 本地流逝 - offset - renderDelay)。
func (tl *TimeLine) RenderTime() time.Duration {
	localElapsed := tl.now().Sub(tl.epoch)
	rt := localElapsed - tl.offset - tl.renderDelay
	if rt < 0 {
		return 0
	}
	return rt
}

// RenderDelay 返回当前配置的渲染延迟。
func (tl *TimeLine) RenderDelay() time.Duration { return tl.renderDelay }

// SetRenderDelay 动态调整渲染延迟(如根据抖动自适应)。
func (tl *TimeLine) SetRenderDelay(d time.Duration) { tl.renderDelay = d }

// ---------- Interpolator ----------

// LerpSnapshot 对两个 Snapshot 做线性插值。t∈[0,1] 时为插值;t>1 时外推。
func LerpSnapshot(a, b Snapshot, t float64) Snapshot {
	return Snapshot{
		ID:      a.ID,
		X:       a.X + (b.X-a.X)*t,
		Y:       a.Y + (b.Y-a.Y)*t,
		VX:      a.VX + (b.VX-a.VX)*t,
		VY:      a.VY + (b.VY-a.VY)*t,
		Angle:   lerpAngle(a.Angle, b.Angle, t),
		Payload: b.Payload,
	}
}

// HermiteSnapshot 三次 Hermite 插值(使用速度作为切线)。t∈[0,1]。
// dt 为两帧时间间隔(秒),用于将速度转为位移量级。
func HermiteSnapshot(a, b Snapshot, t, dt float64) Snapshot {
	t2 := t * t
	t3 := t2 * t
	h00 := 2*t3 - 3*t2 + 1
	h10 := t3 - 2*t2 + t
	h01 := -2*t3 + 3*t2
	h11 := t3 - t2

	return Snapshot{
		ID:      a.ID,
		X:       h00*a.X + h10*(a.VX*dt) + h01*b.X + h11*(b.VX*dt),
		Y:       h00*a.Y + h10*(a.VY*dt) + h01*b.Y + h11*(b.VY*dt),
		VX:      a.VX + (b.VX-a.VX)*t,
		VY:      a.VY + (b.VY-a.VY)*t,
		Angle:   lerpAngle(a.Angle, b.Angle, t),
		Payload: b.Payload,
	}
}

// InterpolateFrame 对两帧做全实体线性插值。t∈[0,1]。
// 只对两帧中都存在的实体做插值;仅在 after 中出现的直接取 after;仅在 before 中的忽略。
func InterpolateFrame(before, after Frame, t float64) []Snapshot {
	bMap := make(map[string]Snapshot, len(before.Entities))
	for _, s := range before.Entities {
		bMap[s.ID] = s
	}
	out := make([]Snapshot, 0, len(after.Entities))
	for _, b := range after.Entities {
		if a, ok := bMap[b.ID]; ok {
			out = append(out, LerpSnapshot(a, b, t))
		} else {
			out = append(out, b)
		}
	}
	return out
}

// lerpAngle 角度插值(处理 360° 环绕)。
func lerpAngle(a, b, t float64) float64 {
	diff := b - a
	for diff > 180 {
		diff -= 360
	}
	for diff < -180 {
		diff += 360
	}
	return a + diff*t
}
