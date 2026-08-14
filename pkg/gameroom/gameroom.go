// Package gameroom 提供 dedicated server 房间生命周期 FSM(Waiting→Running→Draining→Closed)。
// 机制而非策略:不含 Agones/K8s;与 pkg/gameloop、pkg/delayqueue、pkg/fsm 组合。
package gameroom

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/delayqueue"
	"github.com/rushteam/beauty/pkg/fsm"
)

// Phase 是房间阶段。
type Phase string

const (
	PhaseWaiting  Phase = "waiting"
	PhaseReady    Phase = "ready"
	PhaseRunning  Phase = "running"
	PhaseDraining Phase = "draining"
	PhaseClosed   Phase = "closed"
)

// Event 是房间 FSM 事件。
type Event string

const (
	EventPlayerJoined Event = "player_joined"
	EventAllReady     Event = "all_ready"
	EventStart        Event = "start"
	EventDrain        Event = "drain"
	EventEmpty        Event = "empty"
	EventClose        Event = "close"
)

// Spec 描述要创建的房间。
type Spec struct {
	ID         string
	MaxPlayers int
	Meta       map[string]string
}

// Hooks 是房间生命周期回调(轻量,不可阻塞 Fire)。
type Hooks struct {
	OnRunning func(ctx context.Context, roomID string) error
	OnDrain   func(roomID string)
	OnClose   func(roomID string)
}

// Handle 是对外可见的房间句柄。
type Handle struct {
	ID    string
	Phase Phase
}

type roomState struct {
	spec    Spec
	players map[string]struct{}
	fsm     *fsm.FSM[Phase, Event]
}

// Manager 管理多个房间及其 FSM。
type Manager struct {
	mu     sync.Mutex
	rooms  map[string]*roomState
	queue  *delayqueue.Queue
	hooks  Hooks
	startC chan struct{} // closed when manager stopped
}

// Option 配置 Manager。
type Option func(*Manager)

// WithHooks 设置生命周期钩子。
func WithHooks(h Hooks) Option {
	return func(m *Manager) { m.hooks = h }
}

// New 创建 Manager。调用 Stop 释放 delayqueue。
func New(opts ...Option) *Manager {
	m := &Manager{
		rooms:  make(map[string]*roomState),
		queue:  delayqueue.New(),
		startC: make(chan struct{}),
	}
	for _, o := range opts {
		o(m)
	}
	close(m.startC)
	return m
}

// Stop 停止后台 delayqueue。
func (m *Manager) Stop() {
	if m.queue != nil {
		m.queue.Stop()
	}
}

func newRoomFSM() *fsm.FSM[Phase, Event] {
	b := fsm.NewBuilder[Phase, Event](PhaseWaiting)
	b.Allow(PhaseWaiting, EventPlayerJoined, PhaseWaiting)
	b.Allow(PhaseWaiting, EventAllReady, PhaseReady)
	b.Allow(PhaseReady, EventStart, PhaseRunning)
	b.Allow(PhaseRunning, EventDrain, PhaseDraining)
	b.Allow(PhaseRunning, EventEmpty, PhaseClosed)
	b.Allow(PhaseDraining, EventEmpty, PhaseClosed)
	b.Allow(PhaseDraining, EventClose, PhaseClosed)
	b.Allow(PhaseWaiting, EventClose, PhaseClosed)
	b.Allow(PhaseReady, EventClose, PhaseClosed)
	return b.Build()
}

// Allocate 创建并注册房间。
func (m *Manager) Allocate(spec Spec) (*Handle, error) {
	if spec.ID == "" {
		return nil, fmt.Errorf("gameroom: empty room id")
	}
	if spec.MaxPlayers <= 0 {
		spec.MaxPlayers = 16
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rooms[spec.ID]; ok {
		return nil, fmt.Errorf("gameroom: room %q already exists", spec.ID)
	}
	m.rooms[spec.ID] = &roomState{
		spec:    spec,
		players: make(map[string]struct{}),
		fsm:     newRoomFSM(),
	}
	return m.handleLocked(spec.ID), nil
}

// Join 玩家加入房间。
func (m *Manager) Join(roomID, playerID string) error {
	if playerID == "" {
		return fmt.Errorf("gameroom: empty player id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.rooms[roomID]
	if !ok {
		return fmt.Errorf("gameroom: unknown room %q", roomID)
	}
	if rs.fsm.Is(PhaseClosed) || rs.fsm.Is(PhaseDraining) {
		return fmt.Errorf("gameroom: room %q not joinable", roomID)
	}
	if len(rs.players) >= rs.spec.MaxPlayers {
		return fmt.Errorf("gameroom: room %q full", roomID)
	}
	rs.players[playerID] = struct{}{}
	if rs.fsm.Is(PhaseWaiting) {
		_, _ = rs.fsm.Fire(EventPlayerJoined)
		if len(rs.players) >= rs.spec.MaxPlayers {
			_, _ = rs.fsm.Fire(EventAllReady)
		}
	}
	return nil
}

// Leave 玩家离开;空房且 Running 时转 Closed。
func (m *Manager) Leave(roomID, playerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.rooms[roomID]
	if !ok {
		return fmt.Errorf("gameroom: unknown room %q", roomID)
	}
	delete(rs.players, playerID)
	if len(rs.players) == 0 && (rs.fsm.Is(PhaseRunning) || rs.fsm.Is(PhaseDraining)) {
		_, _ = rs.fsm.Fire(EventEmpty)
		if m.hooks.OnClose != nil {
			m.hooks.OnClose(roomID)
		}
		delete(m.rooms, roomID)
	}
	return nil
}

// ScheduleStart 在 delay 后触发 Start(Ready→Running)。
func (m *Manager) ScheduleStart(roomID string, delay time.Duration) error {
	m.mu.Lock()
	rs, ok := m.rooms[roomID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("gameroom: unknown room %q", roomID)
	}
	if !rs.fsm.Is(PhaseReady) && !rs.fsm.Is(PhaseWaiting) {
		m.mu.Unlock()
		return fmt.Errorf("gameroom: room %q not ready to schedule start", roomID)
	}
	m.mu.Unlock()
	key := "start:" + roomID
	m.queue.Schedule(key, delay, func() {
		_ = m.Start(context.Background(), roomID)
	})
	return nil
}

// Start 把房间推进到 Running 并调用 OnRunning 钩子。
func (m *Manager) Start(ctx context.Context, roomID string) error {
	m.mu.Lock()
	rs, ok := m.rooms[roomID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("gameroom: unknown room %q", roomID)
	}
	if rs.fsm.Is(PhaseWaiting) && len(rs.players) > 0 {
		_, _ = rs.fsm.Fire(EventAllReady)
	}
	if rs.fsm.Is(PhaseReady) {
		if _, err := rs.fsm.Fire(EventStart); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if !rs.fsm.Is(PhaseRunning) {
		m.mu.Unlock()
		return fmt.Errorf("gameroom: room %q not in running phase", roomID)
	}
	hooks := m.hooks
	m.mu.Unlock()
	if hooks.OnRunning != nil {
		return hooks.OnRunning(ctx, roomID)
	}
	return nil
}

// Drain 进入 Draining(不再收人,等待对局结束)。
func (m *Manager) Drain(roomID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.rooms[roomID]
	if !ok {
		return fmt.Errorf("gameroom: unknown room %q", roomID)
	}
	if rs.fsm.Is(PhaseClosed) {
		return nil
	}
	if rs.fsm.Is(PhaseRunning) {
		if _, err := rs.fsm.Fire(EventDrain); err != nil {
			return err
		}
	}
	if m.hooks.OnDrain != nil {
		m.hooks.OnDrain(roomID)
	}
	if len(rs.players) == 0 {
		_, _ = rs.fsm.Fire(EventEmpty)
		if m.hooks.OnClose != nil {
			m.hooks.OnClose(roomID)
		}
	}
	return nil
}

// Close 强制关闭房间。
func (m *Manager) Close(roomID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.rooms[roomID]
	if !ok {
		return fmt.Errorf("gameroom: unknown room %q", roomID)
	}
	_ = m.queue.Cancel("start:" + roomID)
	if !rs.fsm.Is(PhaseClosed) {
		_, _ = rs.fsm.Fire(EventClose)
		if m.hooks.OnClose != nil {
			m.hooks.OnClose(roomID)
		}
	}
	delete(m.rooms, roomID)
	return nil
}

// Get 返回房间句柄;不存在时 nil。
func (m *Manager) Get(roomID string) *Handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rooms[roomID]; !ok {
		return nil
	}
	return m.handleLocked(roomID)
}

// Players 返回房间内玩家 ID 快照。
func (m *Manager) Players(roomID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.rooms[roomID]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rs.players))
	for p := range rs.players {
		out = append(out, p)
	}
	return out
}

func (m *Manager) handleLocked(roomID string) *Handle {
	return &Handle{ID: roomID, Phase: m.rooms[roomID].fsm.Current()}
}
