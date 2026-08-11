package topology

import (
	"testing"

	"github.com/rushteam/beauty/pkg/p2p"
)

func TestFullMesh_OnPeerJoin(t *testing.T) {
	topo := FullMesh{}

	pairs := topo.OnPeerJoin("A", nil)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs, got %d", len(pairs))
	}

	pairs = topo.OnPeerJoin("B", []p2p.PeerID{"A"})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].Initiator != "A" || pairs[0].Responder != "B" {
		t.Fatalf("expected A→B, got %s→%s", pairs[0].Initiator, pairs[0].Responder)
	}

	pairs = topo.OnPeerJoin("C", []p2p.PeerID{"A", "B"})
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
}

func TestStar_OnPeerJoin(t *testing.T) {
	topo := Star{Hub: "HOST"}

	pairs := topo.OnPeerJoin("HOST", nil)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs, got %d", len(pairs))
	}

	pairs = topo.OnPeerJoin("A", []p2p.PeerID{"B"})
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs (hub not present), got %d", len(pairs))
	}

	pairs = topo.OnPeerJoin("A", []p2p.PeerID{"HOST"})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].Initiator != "HOST" || pairs[0].Responder != "A" {
		t.Fatalf("expected HOST→A, got %s→%s", pairs[0].Initiator, pairs[0].Responder)
	}

	pairs = topo.OnPeerJoin("HOST", []p2p.PeerID{"A", "B", "C"})
	if len(pairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(pairs))
	}
}

func TestClientServer_DynamicHost(t *testing.T) {
	cs := NewClientServer()

	// 第一个加入的自动成为 host
	pairs := cs.OnPeerJoin("ALICE", nil)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs for first join, got %d", len(pairs))
	}
	if cs.Host() != "ALICE" {
		t.Fatalf("expected host=ALICE, got %s", cs.Host())
	}

	// client 加入:与 host 建连
	pairs = cs.OnPeerJoin("BOB", []p2p.PeerID{"ALICE"})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].Initiator != "ALICE" || pairs[0].Responder != "BOB" {
		t.Fatalf("expected ALICE→BOB, got %s→%s", pairs[0].Initiator, pairs[0].Responder)
	}

	// 另一个 client
	pairs = cs.OnPeerJoin("CAROL", []p2p.PeerID{"ALICE", "BOB"})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].Initiator != "ALICE" || pairs[0].Responder != "CAROL" {
		t.Fatalf("expected ALICE→CAROL, got %s→%s", pairs[0].Initiator, pairs[0].Responder)
	}

	// host 离开
	cs.OnPeerLeave("ALICE", []p2p.PeerID{"BOB", "CAROL"})
	if cs.Host() != "" {
		t.Fatalf("expected host cleared, got %s", cs.Host())
	}
}

func TestWaitForN(t *testing.T) {
	topo := NewWaitForN(3)

	// 前 2 个人加入:不触发
	pairs := topo.OnPeerJoin("A", nil)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs after 1st, got %d", len(pairs))
	}
	pairs = topo.OnPeerJoin("B", []p2p.PeerID{"A"})
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs after 2nd, got %d", len(pairs))
	}
	if topo.WaitingCount() != 2 {
		t.Fatalf("expected 2 waiting, got %d", topo.WaitingCount())
	}

	// 第 3 个人加入:触发全连接(3 对)
	pairs = topo.OnPeerJoin("C", []p2p.PeerID{"A", "B"})
	if len(pairs) != 3 {
		t.Fatalf("expected 3 pairs (full mesh of 3), got %d", len(pairs))
	}
	if topo.WaitingCount() != 0 {
		t.Fatalf("expected 0 waiting after match, got %d", topo.WaitingCount())
	}

	// 验证连接对:A-B, A-C, B-C
	pairSet := make(map[string]bool)
	for _, p := range pairs {
		pairSet[p.Initiator+"-"+p.Responder] = true
	}
	if !pairSet["A-B"] || !pairSet["A-C"] || !pairSet["B-C"] {
		t.Errorf("unexpected pairs: %v", pairs)
	}
}

func TestWaitForN_PeerLeaveBeforeMatch(t *testing.T) {
	topo := NewWaitForN(3)

	topo.OnPeerJoin("A", nil)
	topo.OnPeerJoin("B", []p2p.PeerID{"A"})

	// B 在凑够之前离开
	topo.OnPeerLeave("B", []p2p.PeerID{"A"})
	if topo.WaitingCount() != 1 {
		t.Fatalf("expected 1 waiting after leave, got %d", topo.WaitingCount())
	}

	// 继续加人
	topo.OnPeerJoin("C", []p2p.PeerID{"A"})
	topo.OnPeerJoin("D", []p2p.PeerID{"A", "C"})

	// A + C + D = 3,应触发
	if topo.WaitingCount() != 0 {
		t.Fatalf("expected 0 waiting, got %d", topo.WaitingCount())
	}
}

func TestQueue_Enqueue(t *testing.T) {
	q := NewQueue()

	pair := q.Enqueue("A")
	if pair != nil {
		t.Fatal("expected nil pair for first enqueue")
	}
	if q.WaitingCount() != 1 {
		t.Fatalf("expected 1 waiting, got %d", q.WaitingCount())
	}

	pair = q.Enqueue("B")
	if pair == nil {
		t.Fatal("expected pair, got nil")
	}
	if pair.Initiator != "A" || pair.Responder != "B" {
		t.Fatalf("expected A→B, got %s→%s", pair.Initiator, pair.Responder)
	}
	if q.WaitingCount() != 0 {
		t.Fatalf("expected 0 waiting after match, got %d", q.WaitingCount())
	}

	q.Enqueue("C")
	q.Dequeue("C")
	if q.WaitingCount() != 0 {
		t.Fatalf("expected 0 waiting after dequeue, got %d", q.WaitingCount())
	}
}
