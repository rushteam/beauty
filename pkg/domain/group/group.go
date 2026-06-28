// Package group 提供群组实体:成员角色、邀请/申请审核、公告、最大人数、封禁名单。
//
// 把 examples/clan 里用 relationship+tournament+wallet 现场组合的"公会"语义,
// 沉淀成有边界的一等包——clan 是 demo,group 是可复用的实体封装。
//
// 与 pkg/domain/relationship 的分工:
//   - relationship 是图原语(二部有向图 + 状态编码),无群组语义;
//   - group 是群组语义封装:owner/admin/member 角色、邀请/申请审核流、
//     公告、最大人数、banlist,内部用 relationship.Graph 存成员边
//     (source=groupID, destination=userID, State=角色)。
//
// 角色编码复用 relationship 的常量:StateOwner / StateAdmin / StateActive(member) /
// StatePending(申请中)。group 在其上加业务规则(如 owner 唯一、admin 可踢人、
// banlist 用户不能加入)。
//
// 设计参考 Nakama core_group.go:Group 为一等实体,成员状态可流转。
// 零值不可用,用 New 构造。Store 并发安全。
package group

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/domain/relationship"
)

// 角色常量(复用 relationship 的状态编码,语义别名)。
const (
	RoleMember = relationship.StateActive   // 普通成员
	RoleAdmin  = relationship.StateAdmin    // 管理员
	RoleOwner  = relationship.StateOwner    // 拥有者(唯一)
	RolePending = relationship.StatePending // 申请中(待审核)
)

// Group 一个群组实体。
type Group struct {
	ID          string
	Name        string
	OwnerID     string
	Announcement string
	MaxMembers  int
	CreatedAt   int64
}

// Store 管理群组集合及其成员关系。
type Store struct {
	mu     sync.RWMutex
	groups map[string]*groupState
}

// groupState 一个群组的可变状态:实体 + 成员图 + banlist。
type groupState struct {
	mu      sync.RWMutex
	info    Group
	graph   *relationship.Graph // source=groupID, dest=userID, State=role
	banned  map[string]int64    // userID -> bannedAt(unix nano)
}

// Option 配置 Store(预留,目前无选项)。
type Option func(*config)

type config struct{}

// New 创建群组 Store。
func New(opts ...Option) *Store {
	for _, o := range opts {
		o(&config{})
	}
	return &Store{groups: make(map[string]*groupState)}
}

// Create 创建群组。owner 自动成为成员(StateOwner)。
// MaxMembers<=0 视为不限。
func (s *Store) Create(g Group) error {
	if g.ID == "" || g.OwnerID == "" {
		return errors.New("group: id and owner required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.groups[g.ID]; ok {
		return ErrExists
	}
	if g.CreatedAt == 0 {
		g.CreatedAt = time.Now().UnixNano()
	}
	gs := &groupState{
		info:   g,
		graph:  relationship.New(),
		banned: make(map[string]int64),
	}
	// owner 建立自己→自己? 不对:成员边是 source=groupID, dest=userID。
	_ = gs.graph.AddEdge(relationship.Edge{
		Source: g.ID, Destination: g.OwnerID, State: RoleOwner, Position: g.CreatedAt,
	})
	s.groups[g.ID] = gs
	return nil
}

// Join 用户加入群组(直接加入,无需审核)。
// banned 用户、已达 MaxMembers、已是成员则失败。
func (s *Store) Join(groupID, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	return gs.addMemberLocked(userID, RoleMember)
}

// Request 用户申请加入(进入待审核)。banned/已成员/满员则失败。
// 返回后由 admin/owner 用 Approve(groupID, userID) 审批。
func (s *Store) Request(groupID, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.isBannedLocked(userID) {
		return ErrBanned
	}
	if _, _, err := gs.roleLocked(userID); err == nil {
		return ErrAlreadyMember // 已是成员(含 pending)
	}
	if gs.isFullLocked() {
		return ErrFull
	}
	return gs.graph.AddEdge(relationship.Edge{
		Source: groupID, Destination: userID, State: RolePending, Position: time.Now().UnixNano(),
	})
}

// Approve 审批通过申请:pending → member。仅 owner/admin 可操作。
func (s *Store) Approve(groupID, approver, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(approver, RoleAdmin); err != nil {
		return err
	}
	role, _, err := gs.roleLocked(userID)
	if err != nil || role != RolePending {
		return ErrNotPending
	}
	// 移除 pending 边,加 member 边。
	gs.graph.RemoveEdge(groupID, userID)
	return gs.graph.AddEdge(relationship.Edge{
		Source: groupID, Destination: userID, State: RoleMember, Position: time.Now().UnixNano(),
	})
}

// Reject 拒绝申请:移除 pending 边。仅 owner/admin 可操作。
func (s *Store) Reject(groupID, approver, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(approver, RoleAdmin); err != nil {
		return err
	}
	role, _, err := gs.roleLocked(userID)
	if err != nil || role != RolePending {
		return ErrNotPending
	}
	gs.graph.RemoveEdge(groupID, userID)
	return nil
}

// Leave 用户主动退出。owner 不能退出(须先 TransferOwner)。
func (s *Store) Leave(groupID, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	role, _, err := gs.roleLocked(userID)
	if err != nil {
		return ErrNotMember
	}
	if role == RoleOwner {
		return ErrOwnerCannotLeave
	}
	gs.graph.RemoveEdge(groupID, userID)
	return nil
}

// Kick 踢出成员。仅 owner/admin 可操作,且不能踢同级或更高(admin 不能踢 owner/admin)。
func (s *Store) Kick(groupID, actor, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	actorRole, _, err := gs.roleLocked(actor)
	if err != nil {
		return ErrNotMember
	}
	if actorRole < RoleAdmin {
		return ErrNoPermission
	}
	targetRole, _, err := gs.roleLocked(userID)
	if err != nil {
		return ErrNotMember
	}
	if targetRole >= actorRole {
		return ErrCannotKickPeer // admin 不能踢 admin/owner
	}
	gs.graph.RemoveEdge(groupID, userID)
	return nil
}

// Promote 提升为 admin。仅 owner 可操作。
func (s *Store) Promote(groupID, owner, userID string) error {
	return s.setRole(groupID, owner, userID, RoleAdmin)
}

// Demote 降级 admin 为 member。仅 owner 可操作。
func (s *Store) Demote(groupID, owner, userID string) error {
	return s.setRole(groupID, owner, userID, RoleMember)
}

// setRole 修改成员角色。仅 owner 可操作(promote/demote 都需 owner)。
func (s *Store) setRole(groupID, actor, userID string, newRole int) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(actor, RoleOwner); err != nil {
		return err
	}
	if _, _, err := gs.roleLocked(userID); err != nil {
		return ErrNotMember
	}
	gs.graph.RemoveEdge(groupID, userID)
	return gs.graph.AddEdge(relationship.Edge{
		Source: groupID, Destination: userID, State: newRole, Position: time.Now().UnixNano(),
	})
}

// TransferOwner 转让群主。仅 owner 可操作,新 owner 必须是 member/admin。
func (s *Store) TransferOwner(groupID, owner, newOwner string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(owner, RoleOwner); err != nil {
		return err
	}
	if _, _, err := gs.roleLocked(newOwner); err != nil {
		return ErrNotMember
	}
	// 旧 owner 降为 admin,新 owner 升为 owner。
	gs.graph.RemoveEdge(groupID, owner)
	gs.graph.RemoveEdge(groupID, newOwner)
	now := time.Now().UnixNano()
	_ = gs.graph.AddEdge(relationship.Edge{Source: groupID, Destination: owner, State: RoleAdmin, Position: now})
	_ = gs.graph.AddEdge(relationship.Edge{Source: groupID, Destination: newOwner, State: RoleOwner, Position: now + 1})
	gs.info.OwnerID = newOwner
	return nil
}

// Ban 封禁用户(加入 banlist,立即移除成员关系)。仅 owner/admin 可操作。
// 不能封禁同级或更高(admin 不能 ban owner/admin)。
func (s *Store) Ban(groupID, actor, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	actorRole, _, err := gs.roleLocked(actor)
	if err != nil {
		return ErrNotMember
	}
	if actorRole < RoleAdmin {
		return ErrNoPermission
	}
	targetRole, _, err := gs.roleLocked(userID)
	if err == nil && targetRole >= actorRole {
		return ErrCannotKickPeer
	}
	gs.graph.RemoveEdge(groupID, userID)
	gs.banned[userID] = time.Now().UnixNano()
	return nil
}

// Unban 解除封禁。仅 owner/admin 可操作。
func (s *Store) Unban(groupID, actor, userID string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(actor, RoleAdmin); err != nil {
		return err
	}
	if _, ok := gs.banned[userID]; !ok {
		return ErrNotBanned
	}
	delete(gs.banned, userID)
	return nil
}

// SetAnnouncement 设置群公告。仅 owner/admin 可操作。
func (s *Store) SetAnnouncement(groupID, actor, text string) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(actor, RoleAdmin); err != nil {
		return err
	}
	gs.info.Announcement = text
	return nil
}

// SetMaxMembers 设置最大人数。仅 owner 可操作。
func (s *Store) SetMaxMembers(groupID, owner string, max int) error {
	gs, err := s.get(groupID)
	if err != nil {
		return err
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := gs.checkPrivilegeLocked(owner, RoleOwner); err != nil {
		return err
	}
	gs.info.MaxMembers = max
	return nil
}

// Members 返回成员列表(按角色分组)。含 owner/admin/member(不含 pending)。
func (s *Store) Members(groupID string) (owners, admins, members []string, err error) {
	gs, err := s.get(groupID)
	if err != nil {
		return nil, nil, nil, err
	}
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	for _, e := range gs.graph.Outgoing(groupID, 0, 1<<30, -1) {
		switch e.State {
		case RoleOwner:
			owners = append(owners, e.Destination)
		case RoleAdmin:
			admins = append(admins, e.Destination)
		case RoleMember:
			members = append(members, e.Destination)
		}
	}
	return
}

// Pending 返回待审核申请列表。
func (s *Store) Pending(groupID string) ([]string, error) {
	gs, err := s.get(groupID)
	if err != nil {
		return nil, err
	}
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	out := []string{}
	for _, e := range gs.graph.Outgoing(groupID, 0, 1<<30, RolePending) {
		out = append(out, e.Destination)
	}
	return out, nil
}

// Role 查询某用户的角色。返回 (role, joinedAt, err)。
func (s *Store) Role(groupID, userID string) (role int, joinedAt int64, err error) {
	gs, err2 := s.get(groupID)
	if err2 != nil {
		return 0, 0, err2
	}
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.roleLocked(userID)
}

// Banned 返回封禁名单。
func (s *Store) Banned(groupID string) ([]string, error) {
	gs, err := s.get(groupID)
	if err != nil {
		return nil, err
	}
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	out := slices.Collect(maps.Keys(gs.banned))
	return out, nil
}

// Info 返回群组信息。
func (s *Store) Info(groupID string) (Group, error) {
	gs, err := s.get(groupID)
	if err != nil {
		return Group{}, err
	}
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.info, nil
}

// MemberCount 返回正式成员数(含 owner/admin/member,不含 pending)。
func (s *Store) MemberCount(groupID string) (int, error) {
	gs, err := s.get(groupID)
	if err != nil {
		return 0, err
	}
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	n := 0
	for _, e := range gs.graph.Outgoing(groupID, 0, 1<<30, -1) {
		if e.State == RoleOwner || e.State == RoleAdmin || e.State == RoleMember {
			n++
		}
	}
	return n, nil
}

// --- 内部 helpers ---

// get 取群组状态(加读锁后转写锁由调用方管)。这里只加 Store 级读锁。
func (s *Store) get(groupID string) (*groupState, error) {
	s.mu.RLock()
	gs, ok := s.groups[groupID]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return gs, nil
}

// 注意:groupState 用单独的锁,与 Store 锁分层,避免长时间持 Store 锁。
// 但上面 get 用 RLock 取指针后释放,存在 TOCTOU——群组可能在取锁期间被删。
// group 目前没有 Delete(预留),所以指针稳定,可接受。

// addMemberLocked 加成员(须持 groupState 锁)。
func (gs *groupState) addMemberLocked(userID string, role int) error {
	if gs.isBannedLocked(userID) {
		return ErrBanned
	}
	if _, _, err := gs.roleLocked(userID); err == nil {
		return ErrAlreadyMember
	}
	if gs.isFullLocked() {
		return ErrFull
	}
	return gs.graph.AddEdge(relationship.Edge{
		Source: gs.info.ID, Destination: userID, State: role, Position: time.Now().UnixNano(),
	})
}

// roleLocked 查角色(须持 groupState 锁)。
func (gs *groupState) roleLocked(userID string) (int, int64, error) {
	e, err := gs.graph.Edge(gs.info.ID, userID)
	if err != nil {
		return 0, 0, err
	}
	return e.State, e.Position, nil
}

// isBannedLocked 是否被封禁(须持 groupState 锁)。
func (gs *groupState) isBannedLocked(userID string) bool {
	_, ok := gs.banned[userID]
	return ok
}

// isFullLocked 是否满员(须持 groupState 锁)。MaxMembers<=0 不限。
func (gs *groupState) isFullLocked() bool {
	if gs.info.MaxMembers <= 0 {
		return false
	}
	n := 0
	for _, e := range gs.graph.Outgoing(gs.info.ID, 0, 1<<30, -1) {
		if e.State == RoleOwner || e.State == RoleAdmin || e.State == RoleMember {
			n++
		}
	}
	return n >= gs.info.MaxMembers
}

// checkPrivilegeLocked 检查 actor 角色是否 >= required。不足返回 ErrNoPermission。
func (gs *groupState) checkPrivilegeLocked(actor string, required int) error {
	role, _, err := gs.roleLocked(actor)
	if err != nil {
		return ErrNotMember
	}
	if role < required {
		return ErrNoPermission
	}
	return nil
}

// mu 是 groupState 的锁(覆盖 graph + banned + info 的可变部分)。
// 上面所有方法用 gs.mu.Lock,但 graph 内部也有锁——嵌套调用没问题,
// 因为 graph 的方法不会回调 groupState。

// 错误定义。
var (
	ErrExists          = errors.New("group: already exists")
	ErrNotFound        = errors.New("group: not found")
	ErrAlreadyMember   = errors.New("group: already a member")
	ErrNotMember       = errors.New("group: not a member")
	ErrNotPending      = errors.New("group: not a pending request")
	ErrFull            = errors.New("group: max members reached")
	ErrBanned          = errors.New("group: user is banned")
	ErrNotBanned       = errors.New("group: user is not banned")
	ErrOwnerCannotLeave = errors.New("group: owner cannot leave, transfer first")
	ErrNoPermission    = errors.New("group: no permission")
	ErrCannotKickPeer  = errors.New("group: cannot kick a peer of equal or higher role")
)

// String 便于日志。
func (g Group) String() string {
	return fmt.Sprintf("Group(%s,%s,owner=%s,max=%d)", g.ID, g.Name, g.OwnerID, g.MaxMembers)
}
