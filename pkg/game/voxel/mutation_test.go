package voxel

import "testing"

func TestMutation_RecordCommit(t *testing.T) {
	m := NewMutation()
	if m.Revision() != 0 {
		t.Error("initial revision should be 0")
	}

	m.Record(BlockChange{Pos: BlockPos{0, 0, 0}, OldBlock: Air, NewBlock: 1})
	m.Record(BlockChange{Pos: BlockPos{1, 0, 0}, OldBlock: Air, NewBlock: 2})
	if m.Pending() != 2 {
		t.Errorf("Pending = %d, want 2", m.Pending())
	}

	cm := m.Commit()
	if cm == nil {
		t.Fatal("Commit should return non-nil")
	}
	if cm.Revision != 1 {
		t.Errorf("Revision = %d, want 1", cm.Revision)
	}
	if len(cm.Changes) != 2 {
		t.Errorf("Changes len = %d, want 2", len(cm.Changes))
	}
	if m.Pending() != 0 {
		t.Error("Pending should be 0 after Commit")
	}

	// 空 Commit
	if m.Commit() != nil {
		t.Error("empty Commit should return nil")
	}
}

func TestMutation_RecordSet(t *testing.T) {
	w := NewWorld()
	m := NewMutation()

	old := m.RecordSet(w, BlockPos{5, 5, 5}, 42)
	if old != Air {
		t.Errorf("RecordSet old = %d, want Air", old)
	}
	if w.GetBlock(BlockPos{5, 5, 5}) != 42 {
		t.Error("block should be set in world")
	}
	if m.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", m.Pending())
	}

	// 相同值不记录
	m.RecordSet(w, BlockPos{5, 5, 5}, 42)
	if m.Pending() != 1 {
		t.Errorf("same value Pending = %d, want 1", m.Pending())
	}
}

func TestMutationLog_SinceLatest(t *testing.T) {
	log := NewMutationLog(4)

	for i := 1; i <= 6; i++ {
		log.Append(CommittedMutation{
			Revision: uint64(i),
			Changes:  []BlockChange{{Pos: BlockPos{X: int32(i), Y: 0, Z: 0}}},
		})
	}

	// 最新 Revision
	if log.Latest() != 6 {
		t.Errorf("Latest = %d, want 6", log.Latest())
	}

	// depth=4,保留 3,4,5,6
	changes, ok := log.Since(4)
	if !ok {
		t.Error("Since(4) should be ok")
	}
	if len(changes) != 2 { // rev 5, 6
		t.Errorf("Since(4) len = %d, want 2", len(changes))
	}

	// afterRevision=2,最旧保留的是 rev 3,afterRevision+1=3 == oldest → 刚好无间隙
	changes, ok = log.Since(2)
	if !ok {
		t.Error("Since(2) should be ok (oldest=3, afterRevision+1=3, no gap)")
	}
	if len(changes) != 4 { // rev 3,4,5,6
		t.Errorf("Since(2) len = %d, want 4", len(changes))
	}

	// afterRevision=1,oldest=3,afterRevision+1=2 < 3 → 有间隙(rev 2 丢失)
	_, ok = log.Since(1)
	if ok {
		t.Error("Since(1) should be false (gap: rev 2 lost, oldest=3)")
	}

	// afterRevision=0 → 更旧,也应该失败
	_, ok = log.Since(0)
	if ok {
		t.Error("Since(0) should be false (gap)")
	}

	// 最新后面无更新
	changes, ok = log.Since(6)
	if !ok {
		t.Error("Since(6) should be ok")
	}
	if len(changes) != 0 {
		t.Errorf("Since(6) len = %d, want 0", len(changes))
	}
}

func TestMutationLog_Empty(t *testing.T) {
	log := NewMutationLog(4)
	if log.Latest() != 0 {
		t.Error("empty log Latest should be 0")
	}
	if log.Len() != 0 {
		t.Error("empty log Len should be 0")
	}
	changes, ok := log.Since(0)
	if !ok {
		t.Error("Since on empty log should be ok")
	}
	if len(changes) != 0 {
		t.Error("Since on empty log should return empty")
	}
}
