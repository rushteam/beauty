// Package timeline 提供帧索引事件时间线原语,解决"逻辑计算瞬间完成、表现播放
// 需要时间"的表现与逻辑分离问题。
//
// 解决的问题:回合制/策略战斗中,服务器 1ms 算完"英雄 A 造成 500 伤害 → 目标 B
// 死亡 → 胜利结算",但客户端播放技能动画 + 受击特效 + 死亡动画需要 5 秒。如果
// 服务器算完就下一步,前端还在播第一步的动画;如果服务器硬等客户端播完,浪费资源
// 且容易被外挂利用。
//
// 框架方案:服务器一次算完所有结果,但打上**时间偏移**(offset ticks),组成一条
// 有序的事件时间线下发。客户端按时间线顺序消费,策划调整动画时长时不需要改服务端。
//
// 核心机制:
//   - Timeline[E]:事件时间线,事件按 offset(相对起始的帧偏移)排序;
//   - Append(offset, event):添加事件,可指定"在第 N 帧播放";
//   - After(prevDuration):自动累加偏移,实现"上一步播完后再开始下一步";
//   - Drain(tick):推进时间线到 tick,返回到期的事件(客户端消费);
//   - Snapshot():导出整条时间线(一次性下发给客户端)。
//
// 与相邻原语的关系:
//   - ability 管理"施法阶段"(蓄力→释放→命中),timeline 管理"计算结果的播放时序";
//   - delayqueue/timerqueue 是墙钟定时器(cron 任务、超时踢人),timeline 是游戏帧
//     时间线(战斗回放);
//   - eventbus 是发布-订阅(异步解耦),timeline 是有序时序(同步播放)。
//
// 并发安全:Timeline 不加锁(由单个计算 goroutine 构造,构造完后只读下发)。
// 零值可用:直接声明即可使用。
package timeline

import "slices"

// Entry 是时间线上的一个事件条目。
type Entry[E any] struct {
	// Offset 相对时间线起始的帧偏移(tick 数)。0 = 立即生效。
	Offset int
	// Event 事件内容(由业务定义:伤害结果、buff 变化、表现提示等)。
	Event E
}

// Timeline 是一条帧索引事件时间线。E 是事件类型。
// 零值可用。构造完成后通常一次性下发客户端,或由 Player 逐帧消费。
type Timeline[E any] struct {
	entries []Entry[E]
	cursor  int  // After 用:当前累计偏移
	sorted  bool // entries 是否已排序(添加后失效,Snapshot 后恢复)
}

// Append 在偏移 offset(帧数)处添加事件。offset 可乱序添加,Drain/Snapshot 时排序。
func (t *Timeline[E]) Append(offset int, event E) {
	t.entries = append(t.entries, Entry[E]{Offset: offset, Event: event})
	t.sorted = false
}

// At 在固定帧偏移处添加事件(Append 的别名,语义更清晰)。
func (t *Timeline[E]) At(offset int, event E) {
	t.Append(offset, event)
}

// After 在"当前累计偏移 + duration"处添加事件,并推进内部光标。
// 用于链式构造:"第一步 → 等 30 帧 → 第二步 → 等 20 帧 → 第三步"。
//
//	tl.After(0, hitA)     // offset=0
//	tl.After(30, hitB)    // offset=30
//	tl.After(20, victory) // offset=50
func (t *Timeline[E]) After(duration int, event E) {
	t.cursor += duration
	t.Append(t.cursor, event)
}

// Stagger 在当前 After 光标位置,以固定间隔 interval 添加多个事件(群体技能、
// 连续弹幕)。不推进 After 光标。
//
//	tl.After(0, castStart)
//	tl.Stagger(5, hit1, hit2, hit3) // offset: cursor, cursor+5, cursor+10
//	tl.After(20, castEnd)
func (t *Timeline[E]) Stagger(interval int, events ...E) {
	for i, e := range events {
		t.Append(t.cursor+i*interval, e)
	}
}

// Snapshot 返回排好序的事件列表(一次性下发客户端)。首次调用排序,后续调用
// 如果没有新增事件则复用排序结果。
func (t *Timeline[E]) Snapshot() []Entry[E] {
	if !t.sorted {
		slices.SortStableFunc(t.entries, func(a, b Entry[E]) int {
			return a.Offset - b.Offset
		})
		t.sorted = true
	}
	out := make([]Entry[E], len(t.entries))
	copy(out, t.entries)
	return out
}

// Duration 返回时间线总长度(最大 offset)。
func (t *Timeline[E]) Duration() int {
	if len(t.entries) == 0 {
		return 0
	}
	maxOff := 0
	for _, e := range t.entries {
		if e.Offset > maxOff {
			maxOff = e.Offset
		}
	}
	return maxOff
}

// Len 返回事件总数。
func (t *Timeline[E]) Len() int { return len(t.entries) }

// ---------- Player:逐帧消费器 ----------

// Player 按帧推进消费时间线事件(客户端侧或服务端延迟生效)。
// 非并发安全。
type Player[E any] struct {
	sorted []Entry[E]
	pos    int
	tick   int
}

// NewPlayer 从 Timeline 的快照创建播放器。
func NewPlayer[E any](tl *Timeline[E]) *Player[E] {
	return &Player[E]{sorted: tl.Snapshot()}
}

// NewPlayerFromEntries 从已排序的条目列表创建播放器(如从网络反序列化)。
func NewPlayerFromEntries[E any](entries []Entry[E]) *Player[E] {
	return &Player[E]{sorted: entries}
}

// Advance 推进一帧(tick++),返回本帧到期的事件。
func (p *Player[E]) Advance() []E {
	p.tick++
	return p.eventsAt(p.tick)
}

// AdvanceTo 推进到指定 tick(不回退),返回 (prevTick, tick] 之间到期的所有事件。
func (p *Player[E]) AdvanceTo(tick int) []E {
	if tick <= p.tick {
		return nil
	}
	prev := p.tick
	p.tick = tick
	var out []E
	for p.pos < len(p.sorted) && p.sorted[p.pos].Offset <= tick {
		if p.sorted[p.pos].Offset > prev {
			out = append(out, p.sorted[p.pos].Event)
		}
		p.pos++
	}
	return out
}

func (p *Player[E]) eventsAt(tick int) []E {
	var out []E
	for p.pos < len(p.sorted) && p.sorted[p.pos].Offset <= tick {
		out = append(out, p.sorted[p.pos].Event)
		p.pos++
	}
	return out
}

// Done 是否所有事件已消费完。
func (p *Player[E]) Done() bool { return p.pos >= len(p.sorted) }

// Tick 返回当前帧。
func (p *Player[E]) Tick() int { return p.tick }

// Remaining 返回剩余未消费的事件数。
func (p *Player[E]) Remaining() int {
	r := len(p.sorted) - p.pos
	if r < 0 {
		return 0
	}
	return r
}
