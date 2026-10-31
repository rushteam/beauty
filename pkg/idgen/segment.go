package idgen

import (
	"context"
	"errors"
	"sync"
)

// 本文件提供「号段模式」ID 生成器(业界俗称 Leaf-segment / 号段):每次从持久化后端(通常
// 一张 DB 表)原子地「批发」一段连续 ID 区间到内存,之后在内存里逐个发号,用光再批发下一段。
//
// 相比 Snowflake(本包 Generator):
//   - 号段模式**不依赖时钟**、不需要给每个实例分配唯一 node id,天然规避时钟回拨;
//   - ID 严格单调递增(同实例内)、更紧凑连续,利于分库分表主键 / 订单号;
//   - 代价是依赖一个中心化存储批发号段(但一次批发 step 个,DB 压力 = 请求量/step);
//   - 断网/存储抖动时,只要当前号段没用完就能继续发号,比强依赖中心服务更有韧性。
//
// 双 buffer:当前号段消耗到阈值(默认 90%)时,后台异步预取下一段,使「换段」不阻塞发号,
// 消除切换毛刺。并发安全。

// ErrEmptyRange 后端返回了非正的号段长度。
var ErrEmptyRange = errors.New("idgen: segment store returned empty range")

// SegmentStore 是号段的持久化后端。NextRange 必须**原子**地把 tag 的游标推进一段,
// 返回新分配、未被任何实例用过的连续区间 [start, start+count)。
//
// 典型 MySQL 实现(单行即一个 tag):
//
//	UPDATE id_segment SET max_id = max_id + step WHERE biz_tag = ?;
//	SELECT max_id, step FROM id_segment WHERE biz_tag = ?;   // 同事务
//	// 则 start = max_id - step, count = step
//
// 多实例并发调用由后端的行锁/事务保证互不重叠。
type SegmentStore interface {
	NextRange(ctx context.Context, tag string) (start, count int64, err error)
}

// segment 是内存中的一段已批发区间:cur 为下一个待发值,cur<max 时有效。
type segment struct {
	start, cur, max int64
}

// SegmentID 是某个 tag 的号段发号器。零值不可用,用 NewSegment 构造。
type SegmentID struct {
	store     SegmentStore
	tag       string
	threshold float64 // 预取阈值(当前段消耗比例达到即后台预取)

	mu      sync.Mutex
	cond    *sync.Cond
	cur     *segment
	next    *segment
	loading bool
}

// SegmentOption 配置 SegmentID。
type SegmentOption func(*SegmentID)

// WithPreloadThreshold 设置预取阈值([0,1],默认 0.9):当前号段消耗达此比例时后台预取下一段。
// 设为 >=1 关闭预取(用光才同步批发)。
func WithPreloadThreshold(r float64) SegmentOption {
	return func(s *SegmentID) { s.threshold = r }
}

// NewSegment 创建 tag 的号段发号器。首个号段在首次 Next 时惰性批发。
func NewSegment(store SegmentStore, tag string, opts ...SegmentOption) *SegmentID {
	s := &SegmentID{store: store, tag: tag, threshold: 0.9}
	s.cond = sync.NewCond(&s.mu)
	for _, o := range opts {
		o(s)
	}
	if s.threshold < 0 {
		s.threshold = 0
	}
	return s
}

// Next 返回下一个唯一 ID。当前号段用尽时:优先切到已预取的下一段;若预取仍在途中则等待它
// 完成(保证号段按批发顺序消费,ID 严格单调);都没有才在锁内同步向后端批发(冷启动路径)。
func (s *SegmentID) Next(ctx context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for s.cur == nil || s.cur.cur >= s.cur.max {
		if s.next != nil {
			s.cur, s.next = s.next, nil
			break
		}
		if s.loading {
			s.cond.Wait() // 等预取完成,避免与同步批发争抢导致号段乱序
			continue
		}
		seg, err := s.fetch(ctx)
		if err != nil {
			return 0, err
		}
		s.cur = seg
		break
	}

	v := s.cur.cur
	s.cur.cur++

	s.maybePreload()
	return v, nil
}

// maybePreload 在持锁状态下判断是否该后台预取下一段。
func (s *SegmentID) maybePreload() {
	if s.next != nil || s.loading || s.threshold >= 1 {
		return
	}
	span := s.cur.max - s.cur.start
	used := s.cur.cur - s.cur.start
	if span <= 0 || float64(used) < s.threshold*float64(span) {
		return
	}
	s.loading = true
	go s.preload()
}

// preload 后台批发下一段。失败则放弃(下次用尽时再同步批发),不影响当前发号。
func (s *SegmentID) preload() {
	seg, err := s.fetch(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loading = false
	if err == nil && s.next == nil {
		s.next = seg
	}
	s.cond.Broadcast() // 唤醒可能在等待本次预取的 Next
}

// fetch 向后端批发一段(不持锁调用,调用方负责持锁语义见各处)。
func (s *SegmentID) fetch(ctx context.Context) (*segment, error) {
	start, count, err := s.store.NextRange(ctx, s.tag)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		return nil, ErrEmptyRange
	}
	return &segment{start: start, cur: start, max: start + count}, nil
}

// MemorySegmentStore 是纯内存的 SegmentStore:进程内按固定 step 批发,供测试与单机场景使用
// (重启后游标丢失,不保证跨进程唯一)。并发安全。
type MemorySegmentStore struct {
	step int64
	mu   sync.Mutex
	next map[string]int64
}

// NewMemorySegmentStore 创建内存号段后端,每次批发 step 个(step<=0 取 1000)。
func NewMemorySegmentStore(step int64) *MemorySegmentStore {
	if step <= 0 {
		step = 1000
	}
	return &MemorySegmentStore{step: step, next: map[string]int64{}}
}

// NextRange 批发下一段。
func (m *MemorySegmentStore) NextRange(_ context.Context, tag string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := m.next[tag]
	m.next[tag] = start + m.step
	return start, m.step, nil
}
