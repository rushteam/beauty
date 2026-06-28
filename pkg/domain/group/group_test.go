package group_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/group"
)

func newGroup(id, name, owner string, max int) group.Group {
	return group.Group{ID: id, Name: name, OwnerID: owner, MaxMembers: max}
}

func TestGroup_Create_OwnerIsMember(t *testing.T) {
	s := group.New()
	if err := s.Create(newGroup("g1", "G1", "alice", 0)); err != nil {
		t.Fatal(err)
	}
	role, _, _ := s.Role("g1", "alice")
	if role != group.RoleOwner {
		t.Fatalf("owner role want %d, got %d", group.RoleOwner, role)
	}
	if n, _ := s.MemberCount("g1"); n != 1 {
		t.Fatalf("count want 1, got %d", n)
	}
}

func TestGroup_Create_Duplicate(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	if err := s.Create(newGroup("g1", "G1", "bob", 0)); !errors.Is(err, group.ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
}

func TestGroup_Join_And_Leave(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	if err := s.Join("g1", "bob"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.MemberCount("g1"); n != 2 {
		t.Fatalf("count want 2, got %d", n)
	}
	if err := s.Leave("g1", "bob"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.MemberCount("g1"); n != 1 {
		t.Fatalf("count after leave want 1, got %d", n)
	}
}

func TestGroup_Join_AlreadyMember(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	if err := s.Join("g1", "bob"); !errors.Is(err, group.ErrAlreadyMember) {
		t.Fatalf("want ErrAlreadyMember, got %v", err)
	}
}

func TestGroup_Join_Full(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 1))
	if err := s.Join("g1", "bob"); !errors.Is(err, group.ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}

func TestGroup_Request_Approve_Flow(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	if err := s.Request("g1", "bob"); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Pending("g1")
	if len(pending) != 1 || pending[0] != "bob" {
		t.Fatalf("pending want [bob], got %v", pending)
	}
	// 非 admin 不能审批。
	if err := s.Approve("g1", "carol", "bob"); !errors.Is(err, group.ErrNotMember) {
		t.Fatalf("non-member approve want ErrNotMember, got %v", err)
	}
	if err := s.Approve("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	role, _, _ := s.Role("g1", "bob")
	if role != group.RoleMember {
		t.Fatalf("after approve role want member, got %d", role)
	}
}

func TestGroup_Request_Reject(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Request("g1", "bob")
	if err := s.Reject("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Pending("g1")
	if len(pending) != 0 {
		t.Fatalf("pending after reject want empty, got %v", pending)
	}
}

func TestGroup_OwnerCannotLeave(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	if err := s.Leave("g1", "alice"); !errors.Is(err, group.ErrOwnerCannotLeave) {
		t.Fatalf("want ErrOwnerCannotLeave, got %v", err)
	}
}

func TestGroup_Kick_Permission(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	// member 不能踢人。
	if err := s.Kick("g1", "bob", "alice"); !errors.Is(err, group.ErrNoPermission) {
		t.Fatalf("member kick want ErrNoPermission, got %v", err)
	}
	// owner 踢 member。
	if err := s.Kick("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
}

func TestGroup_Admin_Cannot_Kick_Peer(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	s.Join("g1", "carol")
	s.Promote("g1", "alice", "bob") // bob=admin
	// admin bob 不能踢 admin carol? carol 是 member,能踢。
	// 但 admin 不能踢另一个 admin:先 promote carol。
	s.Promote("g1", "alice", "carol") // carol=admin
	if err := s.Kick("g1", "bob", "carol"); !errors.Is(err, group.ErrCannotKickPeer) {
		t.Fatalf("admin kick admin want ErrCannotKickPeer, got %v", err)
	}
	// admin 不能踢 owner。
	if err := s.Kick("g1", "bob", "alice"); !errors.Is(err, group.ErrCannotKickPeer) {
		t.Fatalf("admin kick owner want ErrCannotKickPeer, got %v", err)
	}
}

func TestGroup_Promote_Demote(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	if err := s.Promote("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	role, _, _ := s.Role("g1", "bob")
	if role != group.RoleAdmin {
		t.Fatalf("promoted role want admin, got %d", role)
	}
	// 非 owner 不能 demote。
	if err := s.Demote("g1", "bob", "bob"); !errors.Is(err, group.ErrNoPermission) {
		t.Fatalf("non-owner demote want ErrNoPermission, got %v", err)
	}
	if err := s.Demote("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	role, _, _ = s.Role("g1", "bob")
	if role != group.RoleMember {
		t.Fatalf("demoted role want member, got %d", role)
	}
}

func TestGroup_TransferOwner(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	if err := s.TransferOwner("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Info("g1")
	if info.OwnerID != "bob" {
		t.Fatalf("owner want bob, got %s", info.OwnerID)
	}
	role, _, _ := s.Role("g1", "alice")
	if role != group.RoleAdmin {
		t.Fatalf("old owner want admin, got %d", role)
	}
	role, _, _ = s.Role("g1", "bob")
	if role != group.RoleOwner {
		t.Fatalf("new owner want owner, got %d", role)
	}
}

func TestGroup_Ban_Unban(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	if err := s.Ban("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	// 被封后不能再 join。
	if err := s.Join("g1", "bob"); !errors.Is(err, group.ErrBanned) {
		t.Fatalf("banned join want ErrBanned, got %v", err)
	}
	banned, _ := s.Banned("g1")
	if len(banned) != 1 || banned[0] != "bob" {
		t.Fatalf("banned want [bob], got %v", banned)
	}
	if err := s.Unban("g1", "alice", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join("g1", "bob"); err != nil {
		t.Fatalf("join after unban should succeed, got %v", err)
	}
}

func TestGroup_Ban_Admin_Cannot_Ban_Peer(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	s.Join("g1", "carol")
	s.Promote("g1", "alice", "bob")
	s.Promote("g1", "alice", "carol")
	if err := s.Ban("g1", "bob", "carol"); !errors.Is(err, group.ErrCannotKickPeer) {
		t.Fatalf("admin ban admin want ErrCannotKickPeer, got %v", err)
	}
}

func TestGroup_SetAnnouncement(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	// member 不能设公告。
	if err := s.SetAnnouncement("g1", "bob", "hi"); !errors.Is(err, group.ErrNoPermission) {
		t.Fatalf("member set announcement want ErrNoPermission, got %v", err)
	}
	if err := s.SetAnnouncement("g1", "alice", "welcome"); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Info("g1")
	if info.Announcement != "welcome" {
		t.Fatalf("announcement want welcome, got %q", info.Announcement)
	}
}

func TestGroup_SetMaxMembers(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	if err := s.SetMaxMembers("g1", "alice", 3); err != nil {
		t.Fatal(err)
	}
	s.Join("g1", "bob")
	s.Join("g1", "carol")
	if err := s.Join("g1", "dave"); !errors.Is(err, group.ErrFull) {
		t.Fatalf("4th join want ErrFull, got %v", err)
	}
}

func TestGroup_Members_ByRole(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	s.Join("g1", "bob")
	s.Join("g1", "carol")
	s.Promote("g1", "alice", "bob")
	owners, admins, members, _ := s.Members("g1")
	if len(owners) != 1 || owners[0] != "alice" {
		t.Fatalf("owners want [alice], got %v", owners)
	}
	if len(admins) != 1 || admins[0] != "bob" {
		t.Fatalf("admins want [bob], got %v", admins)
	}
	if len(members) != 1 || members[0] != "carol" {
		t.Fatalf("members want [carol], got %v", members)
	}
}

func TestGroup_NotFound(t *testing.T) {
	s := group.New()
	if err := s.Join("nope", "bob"); !errors.Is(err, group.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGroup_Concurrent(t *testing.T) {
	s := group.New()
	s.Create(newGroup("g1", "G1", "alice", 0))
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			s.Join("g1", "u")
			s.Leave("g1", "u")
		})
	}
	wg.Wait()
	if n, _ := s.MemberCount("g1"); n != 1 {
		t.Fatalf("after concurrent join/leave want 1 (owner), got %d", n)
	}
}
