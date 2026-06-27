package party_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/party"
)

func newTestParty(open bool, maxSize int) (*party.Party, *[]party.Snapshot) {
	var mu sync.Mutex
	snaps := []party.Snapshot{}
	p := party.New("p1", party.Member{UserID: "leader", Username: "L"},
		func(s party.Snapshot) {
			mu.Lock()
			snaps = append(snaps, s)
			mu.Unlock()
		},
		party.WithOpen(open), party.WithMaxSize(maxSize),
	)
	return p, &snaps
}

func TestParty_OpenAutoJoin(t *testing.T) {
	p, snaps := newTestParty(true, 0)
	if err := p.RequestJoin(party.Member{UserID: "u1"}); err != nil {
		t.Fatalf("join: %v", err)
	}
	if p.Count() != 2 {
		t.Fatalf("count=%d", p.Count())
	}
	if len(p.Snapshot().JoinRequests) != 0 {
		t.Fatal("open party should not queue requests")
	}
	// 应广播两次(New 时不广播,join 时广播 1 次)。
	if len(*snaps) != 1 {
		t.Fatalf("snapshots=%d, want 1", len(*snaps))
	}
}

func TestParty_PrivateQueueAndAccept(t *testing.T) {
	p, _ := newTestParty(false, 0)
	if err := p.RequestJoin(party.Member{UserID: "u1"}); err != nil {
		t.Fatalf("request: %v", err)
	}
	// 还不是成员。
	if p.Count() != 1 {
		t.Fatalf("count=%d before accept", p.Count())
	}
	if len(p.Snapshot().JoinRequests) != 1 {
		t.Fatal("request not queued")
	}
	// 非队长无权 Accept。
	if err := p.Accept("u1", "u1"); !errors.Is(err, party.ErrNotLeader) {
		t.Fatalf("want ErrNotLeader, got %v", err)
	}
	if err := p.Accept("leader", "u1"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if p.Count() != 2 {
		t.Fatalf("count=%d after accept", p.Count())
	}
	// 再次 Accept 已无此请求。
	if err := p.Accept("leader", "u1"); err == nil {
		t.Fatal("want error for missing request")
	}
}

func TestParty_AlreadyMember(t *testing.T) {
	p, _ := newTestParty(true, 0)
	if err := p.RequestJoin(party.Member{UserID: "leader"}); err == nil {
		t.Fatal("want ErrAlreadyMember")
	}
}

func TestParty_Full(t *testing.T) {
	p, _ := newTestParty(true, 2) // leader + 1
	if err := p.RequestJoin(party.Member{UserID: "u1"}); err != nil {
		t.Fatalf("u1 join: %v", err)
	}
	if err := p.RequestJoin(party.Member{UserID: "u2"}); !errors.Is(err, party.ErrPartyFull) {
		t.Fatalf("want ErrPartyFull, got %v", err)
	}
}

func TestParty_PrivateReserveSeat(t *testing.T) {
	// maxSize=2:leader 占 1,1 个请求预留 1,第 2 个请求应满。
	p, _ := newTestParty(false, 2)
	if err := p.RequestJoin(party.Member{UserID: "u1"}); err != nil {
		t.Fatalf("u1 request: %v", err)
	}
	if err := p.RequestJoin(party.Member{UserID: "u2"}); !errors.Is(err, party.ErrPartyFull) {
		t.Fatalf("want ErrPartyFull for reserved seat, got %v", err)
	}
}

func TestParty_RemoveMember(t *testing.T) {
	p, _ := newTestParty(true, 0)
	p.RequestJoin(party.Member{UserID: "u1"})
	if err := p.Remove("leader", "u1"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if p.Count() != 1 {
		t.Fatalf("count=%d", p.Count())
	}
}

func TestParty_SelfLeave(t *testing.T) {
	p, _ := newTestParty(true, 0)
	p.RequestJoin(party.Member{UserID: "u1"})
	// u1 自离。
	if err := p.Remove("u1", "u1"); err != nil {
		t.Fatalf("self leave: %v", err)
	}
	if p.Count() != 1 {
		t.Fatalf("count=%d", p.Count())
	}
}

func TestParty_LeaderLeavePromote(t *testing.T) {
	p, _ := newTestParty(true, 0)
	p.RequestJoin(party.Member{UserID: "u1"})
	// leader 离开,应自动转让给 u1。
	if err := p.Remove("leader", "leader"); err != nil {
		t.Fatalf("leader leave: %v", err)
	}
	if p.LeaderID() != "u1" {
		t.Fatalf("new leader=%s, want u1", p.LeaderID())
	}
}

func TestParty_LeaderLeaveEmpty(t *testing.T) {
	p, _ := newTestParty(true, 0)
	if err := p.Remove("leader", "leader"); err != nil {
		t.Fatalf("leader leave: %v", err)
	}
	if !p.Stopped() {
		t.Fatal("should be stopped")
	}
}

func TestParty_Promote(t *testing.T) {
	p, _ := newTestParty(true, 0)
	p.RequestJoin(party.Member{UserID: "u1"})
	// 非 leader 无权 promote。
	if err := p.Promote("u1", "u1"); !errors.Is(err, party.ErrNotLeader) {
		t.Fatalf("want ErrNotLeader, got %v", err)
	}
	if err := p.Promote("leader", "u1"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if p.LeaderID() != "u1" {
		t.Fatalf("leader=%s", p.LeaderID())
	}
}

func TestParty_OnChangeBroadcast(t *testing.T) {
	var mu sync.Mutex
	snaps := []party.Snapshot{}
	p := party.New("p", party.Member{UserID: "L"}, func(s party.Snapshot) {
		mu.Lock()
		snaps = append(snaps, s)
		mu.Unlock()
	}, party.WithOpen(true))
	p.RequestJoin(party.Member{UserID: "u1"})
	p.Remove("L", "u1")
	mu.Lock()
	defer mu.Unlock()
	// 每次 join/remove 各广播一次。
	if len(snaps) != 2 {
		t.Fatalf("snapshots=%d, want 2", len(snaps))
	}
	if len(snaps[0].Members) != 2 || len(snaps[1].Members) != 1 {
		t.Fatalf("members: %+v", snaps)
	}
}

func TestParty_Concurrent(t *testing.T) {
	p, _ := newTestParty(true, 0)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = p.RequestJoin(party.Member{UserID: "u" + itoa(i)})
		}(i)
	}
	wg.Wait()
	if p.Count() != 21 {
		t.Fatalf("count=%d, want 21", p.Count())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
