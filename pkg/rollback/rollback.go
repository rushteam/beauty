// Package rollback 提供 GGPO 风格的回滚重演算(rollback netcode)编排原语,
// 解决"客户端预测 + 服务器权威"架构下的延迟消除问题。
//
// 解决的问题:FPS/格斗/MOBA 类实时游戏中,玩家按下方向键时客户端不等待服务器
// 确认就立即移动(预测),当服务器随后下发的权威状态与本地预测不一致时,需要
// "回滚到过去某帧 → 用权威状态覆盖 → 重放所有中间帧的输入"来无感纠正偏差。
// 这就是回滚重演算(rollback & resimulate)。
//
// 核心机制:
//   - Simulator[S, I]:业务需实现此接口——给定当前状态 S 和一帧的输入 I,返回新状态。
//   - Session[S, I]:本包的核心,管理预测帧、快照保存、服务器确认、回滚+重放。
//   - 本包只做编排(何时存快照、何时回滚、如何重放),不管网络传输(那是 gameloop/
//     ws/quic 的事)、不管确定性(那是 fixedpoint 的事)、不管渲染平滑(那是客户端的事)。
//
// 与相邻原语的关系:
//   - snapbuf 是"固定深度环形快照缓冲",rollback 内部使用它存历史快照;
//   - inputclock 用于帧映射与 RTT 估算,rollback 按需组合;
//   - gameloop 驱动 tick,rollback 在 OnTick 内调用;
//   - fixedpoint 保证 Simulate 的确定性——"相同 S + I → 相同 S'"。
//
// 并发安全:Session 不加锁(单一游戏循环线程内调用)。
// 零值不可用:用 NewSession 构造。
package rollback

// Simulator 是业务的帧推进器:给定当前世界状态 S 和一帧输入切片 I,返回下一帧状态。
// 此函数必须是确定性的(相同 S + I → 相同返回值),否则回滚后会再次不同步。
type Simulator[S, I any] interface {
	Simulate(state S, frame uint64, inputs []I) S
}

// SimulatorFunc 把普通函数适配为 Simulator。
type SimulatorFunc[S, I any] func(state S, frame uint64, inputs []I) S

func (f SimulatorFunc[S, I]) Simulate(state S, frame uint64, inputs []I) S {
	return f(state, frame, inputs)
}

// config 配置。
type config struct {
	maxRollback int
	snapDepth   int
}

// Option 配置 Session。
type Option func(*config)

// WithMaxRollback 设置最大允许回滚帧数(默认 8)。超出则放弃回滚,直接跳到
// 服务器状态(会有跳帧感,但优于长时间卡顿)。
func WithMaxRollback(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxRollback = n
		}
	}
}

// WithSnapDepth 设置快照保存深度(默认 32)。
func WithSnapDepth(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.snapDepth = n
		}
	}
}

// Session 管理一个预测+回滚会话。S 是世界状态类型,I 是单帧输入类型。
// 非并发安全(由单个游戏循环驱动)。
type Session[S, I any] struct {
	sim         Simulator[S, I]
	maxRollback int

	// 帧管理
	confirmedFrame uint64 // 最后一个被服务器确认的帧
	currentFrame   uint64 // 当前(含预测)已推进到的帧

	// 状态
	confirmedState S // 最后一个服务器权威状态
	currentState   S // 当前(可能含预测)的状态

	// 快照:按帧号索引,用于回滚时恢复
	snaps map[uint64]S

	// 输入历史:按帧号索引,用于回滚后重放
	inputs map[uint64][]I

	// 统计
	stats Stats
}

// Stats 回滚统计,用于监控/调参。
type Stats struct {
	Rollbacks      int    // 累计回滚次数
	MaxRollbackN   int    // 单次最大回滚帧数
	TotalResim     int    // 累计重演算帧数
	Corrections    int    // 预测正确不需要回滚的次数
	ForcedResets   int    // 超出 maxRollback 被强制跳帧的次数
	CurrentFrame   uint64 // 当前帧
	ConfirmedFrame uint64 // 已确认帧
}

// NewSession 创建回滚会话。initial 是初始世界状态,sim 是帧推进器。
func NewSession[S, I any](initial S, sim Simulator[S, I], opts ...Option) *Session[S, I] {
	cfg := config{maxRollback: 8, snapDepth: 32}
	for _, o := range opts {
		o(&cfg)
	}
	s := &Session[S, I]{
		sim:            sim,
		maxRollback:    cfg.maxRollback,
		confirmedState: initial,
		currentState:   initial,
		snaps:          make(map[uint64]S, cfg.snapDepth),
		inputs:         make(map[uint64][]I),
	}
	s.snaps[0] = initial
	return s
}

// Advance 本地预测推进一帧:用 predicted 输入推进状态,保存快照。
// 返回推进后的帧号和状态。通常在客户端每帧调用。
func (s *Session[S, I]) Advance(predicted []I) (frame uint64, state S) {
	s.currentFrame++
	frame = s.currentFrame
	s.inputs[frame] = predicted
	s.currentState = s.sim.Simulate(s.currentState, frame, predicted)
	s.snaps[frame] = s.currentState
	s.gc()
	s.stats.CurrentFrame = frame
	return frame, s.currentState
}

// Confirm 接收服务器权威确认:frame 是服务器确认的帧号,serverState 是权威状态,
// confirmedInputs 是该帧的权威输入(用于替换本地预测输入后重放)。
//
// 返回值:
//   - rolled: 是否发生了回滚
//   - resimN: 重演算的帧数(0 表示预测正确或被强制跳帧)
//   - state: 纠正后的当前状态
func (s *Session[S, I]) Confirm(frame uint64, serverState S, confirmedInputs []I) (rolled bool, resimN int, state S) {
	if frame <= s.confirmedFrame {
		return false, 0, s.currentState
	}
	s.confirmedFrame = frame
	s.confirmedState = serverState
	s.stats.ConfirmedFrame = frame
	s.inputs[frame] = confirmedInputs

	gap := int(s.currentFrame - frame)
	if gap <= 0 {
		// 服务器确认的帧 >= 当前帧:直接采纳服务器状态
		s.currentFrame = frame
		s.currentState = serverState
		s.snaps[frame] = serverState
		s.stats.Corrections++
		return false, 0, s.currentState
	}

	if gap > s.maxRollback {
		// 差距太大,放弃回滚,强制跳到服务器状态
		s.currentFrame = frame
		s.currentState = serverState
		s.snaps[frame] = serverState
		s.stats.ForcedResets++
		return true, 0, s.currentState
	}

	// 回滚到 frame,用 serverState 覆盖,然后重放 frame+1 ... currentFrame
	s.stats.Rollbacks++
	if gap > s.stats.MaxRollbackN {
		s.stats.MaxRollbackN = gap
	}

	st := serverState
	s.snaps[frame] = st
	for f := frame + 1; f <= s.currentFrame; f++ {
		inp := s.inputs[f]
		st = s.sim.Simulate(st, f, inp)
		s.snaps[f] = st
		resimN++
	}
	s.currentState = st
	s.stats.TotalResim += resimN
	return true, resimN, s.currentState
}

// State 返回当前状态(可能含预测)。
func (s *Session[S, I]) State() S { return s.currentState }

// Frame 返回当前帧号。
func (s *Session[S, I]) Frame() uint64 { return s.currentFrame }

// ConfirmedFrame 返回最后确认帧号。
func (s *Session[S, I]) ConfirmedFrame() uint64 { return s.confirmedFrame }

// PredictionGap 返回预测领先服务器确认的帧数。
func (s *Session[S, I]) PredictionGap() int {
	return int(s.currentFrame - s.confirmedFrame)
}

// Stats 返回累计统计。
func (s *Session[S, I]) Stats() Stats { return s.stats }

// SnapshotAt 返回指定帧的快照(如果仍在缓冲中)。
func (s *Session[S, I]) SnapshotAt(frame uint64) (S, bool) {
	snap, ok := s.snaps[frame]
	return snap, ok
}

// gc 清理过旧的快照和输入(保留 confirmed 帧之后的)。
func (s *Session[S, I]) gc() {
	cutoff := s.confirmedFrame
	if cutoff == 0 {
		return
	}
	for f := range s.snaps {
		if f < cutoff {
			delete(s.snaps, f)
		}
	}
	for f := range s.inputs {
		if f < cutoff {
			delete(s.inputs, f)
		}
	}
}
