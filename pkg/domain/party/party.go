// Package party 提供无权威的多人小团体原语:Leader + Members + 加入请求队列,
// 成员变更时广播给全员。与 pkg/match 的权威状态机互补——party 是用户意愿驱动的
// 临时协作组(好友开黑、小队、临时房间),状态由成员操作驱动,无固定帧率 tick。
//
// 设计参考 Nakama server/party_handler.go:
//   - Leader 唯一,可 Promote 转让、Remove 踢人;
//   - JoinRequests 队列:private party 需 Leader Accept;open party 自动加入;
//   - 座位预留(Reserve/Release):处理"加入中"与"已加入"的竞态,支持容量限制;
//   - 状态同步:每次变更广播给全员(通过 OnChange 回调,业务接 router.SendToStream)。
//
// 与 pkg/match 的区别:match 是服务端权威状态机(固定 tick、串行输入、背压降级),
// 适合对战;party 是成员驱动的轻量协作(无 tick、操作即广播),适合组队/小队。
//
// 零值不可用,用 New 构造。Party 并发安全。
package party

import (
	"errors"
	"sync"
)

// Member 是一个派对成员。
type Member struct {
	UserID    string
	SessionID string
	Username  string
}

// JoinRequest 是一个待审核的加入请求。
type JoinRequest struct {
	Member   Member
	Reserved bool // 是否已预留座位(防止 Accept 时超容量)
}

// Snapshot 是派对当前状态的不可变快照,用于广播。
type Snapshot struct {
	ID           string
	LeaderID     string
	Members      []Member
	JoinRequests []JoinRequest
	Open         bool   // open:任何人直接加入;false:需 Leader Accept
	MaxSize      int    // 最大成员数(含预留),0 不限
}

// OnChange 成员/状态变更时的回调。业务在此调 router.SendToStream 广播给全员。
type OnChange func(snap Snapshot)

// Party 一个派对实例。
type Party struct {
	mu       sync.Mutex
	id       string
	leader   Member
	members  map[string]*Member // key = UserID
	requests []JoinRequest
	open     bool
	maxSize  int
	reserved int // 已预留座位数
	onChange OnChange
	stopped  bool
}

// ErrPartyFull 派对已满(含预留)。
var ErrPartyFull = errors.New("party: full")

// ErrNotLeader 非队长无权操作。
var ErrNotLeader = errors.New("party: not leader")

// ErrAlreadyMember 已是成员。
var ErrAlreadyMember = errors.New("party: already a member")

// Option 配置 Party。
type Option func(*config)

type config struct {
	open    bool
	maxSize int
}

// WithOpen 设置为开放派对(任何人直接加入,无需审核)。
func WithOpen(b bool) Option { return func(c *config) { c.open = b } }

// WithMaxSize 最大成员数(含预留座位)。
func WithMaxSize(n int) Option { return func(c *config) { c.maxSize = n } }

// New 创建派对。leader 为首任队长(自动加入成员)。
func New(id string, leader Member, onChange OnChange, opts ...Option) *Party {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	p := &Party{
		id:       id,
		leader:   leader,
		members:  map[string]*Member{leader.UserID: &leader},
		open:     cfg.open,
		maxSize:  cfg.maxSize,
		onChange: onChange,
	}
	return p
}

// RequestJoin 请求加入。
//   - open party:直接加入(若未满);
//   - private party:进入 JoinRequests 队列(预留座位),等 Leader Accept。
func (p *Party) RequestJoin(m Member) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return errors.New("party: stopped")
	}
	if _, ok := p.members[m.UserID]; ok {
		return ErrAlreadyMember
	}
	if p.open {
		if err := p.canAddLocked(); err != nil {
			return err
		}
		p.members[m.UserID] = &m
		p.notifyLocked()
		return nil
	}
	// private:加入请求队列,预留座位防止 Accept 时超容量。
	if err := p.canAddLocked(); err != nil {
		return err
	}
	// 避免重复请求。
	for _, r := range p.requests {
		if r.Member.UserID == m.UserID {
			return errors.New("party: already requested")
		}
	}
	p.requests = append(p.requests, JoinRequest{Member: m, Reserved: true})
	p.reserved++
	p.notifyLocked()
	return nil
}

// Accept 由 Leader 接受一个加入请求。
func (p *Party) Accept(leaderID, userID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if leaderID != p.leader.UserID {
		return ErrNotLeader
	}
	idx := -1
	for i, r := range p.requests {
		if r.Member.UserID == userID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("party: no such join request")
	}
	r := p.requests[idx]
	p.requests = append(p.requests[:idx], p.requests[idx+1:]...)
	if r.Reserved {
		p.reserved--
	}
	p.members[r.Member.UserID] = &r.Member
	p.notifyLocked()
	return nil
}

// Remove 由 Leader(或成员自离)移除成员。成员自离时 leaderID 可等于 userID。
func (p *Party) Remove(leaderID, userID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	isSelf := leaderID == userID
	if !isSelf && leaderID != p.leader.UserID {
		return ErrNotLeader
	}
	m, ok := p.members[userID]
	if !ok {
		return errors.New("party: no such member")
	}
	delete(p.members, userID)
	// 队长离开:自动转让给最早加入的剩余成员;无剩余则标记停止。
	if userID == p.leader.UserID {
		for _, next := range p.members {
			p.leader = *next
			break
		}
		if len(p.members) == 0 {
			p.stopped = true
		}
	}
	_ = m
	p.notifyLocked()
	return nil
}

// Promote 由 Leader 转让队长。
func (p *Party) Promote(leaderID, newLeaderID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if leaderID != p.leader.UserID {
		return ErrNotLeader
	}
	m, ok := p.members[newLeaderID]
	if !ok {
		return errors.New("party: no such member")
	}
	p.leader = *m
	p.notifyLocked()
	return nil
}

// Snapshot 返回当前状态快照(不可变拷贝)。
func (p *Party) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

// Members 返回成员列表(拷贝)。
func (p *Party) Members() []Member {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Member, 0, len(p.members))
	for _, m := range p.members {
		out = append(out, *m)
	}
	return out
}

// LeaderID 返回队长 UserID。
func (p *Party) LeaderID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leader.UserID
}

// Stopped 派对是否已停止(队长离开且无成员)。
func (p *Party) Stopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

// Count 当前成员数(不含预留)。
func (p *Party) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.members)
}

func (p *Party) canAddLocked() error {
	if p.maxSize <= 0 {
		return nil
	}
	if len(p.members)+p.reserved >= p.maxSize {
		return ErrPartyFull
	}
	return nil
}

func (p *Party) notifyLocked() {
	if p.onChange == nil {
		return
	}
	p.onChange(p.snapshotLocked())
}

func (p *Party) snapshotLocked() Snapshot {
	members := make([]Member, 0, len(p.members))
	for _, m := range p.members {
		members = append(members, *m)
	}
	reqs := make([]JoinRequest, len(p.requests))
	copy(reqs, p.requests)
	return Snapshot{
		ID:           p.id,
		LeaderID:     p.leader.UserID,
		Members:      members,
		JoinRequests: reqs,
		Open:         p.open,
		MaxSize:      p.maxSize,
	}
}
