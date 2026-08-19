package cmdlog_test

import (
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/cmdlog"
)

type state = int  // 世界状态:简单用位置
type cmd = string // 指令:简单用字符串

func TestRecord_And_Recover_ShortDisconnect(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()

	// 存快照
	log.Checkpoint(10, 100)

	// 记录指令 f11-f20
	for f := uint64(11); f <= 20; f++ {
		log.Record(f, []cmd{"move"})
	}

	// 玩家在 f15 断线,f20 重连
	r, ok := log.Recover(15)
	if !ok {
		t.Fatal("recover should succeed")
	}
	if r.FullReset {
		t.Fatal("should be catch-up, not full reset")
	}
	if r.SnapshotFrame != 10 {
		t.Fatalf("snapshot frame = %d, want 10", r.SnapshotFrame)
	}
	if r.Snapshot != 100 {
		t.Fatalf("snapshot = %d, want 100", r.Snapshot)
	}
	// 指令流:f11-f20(快照后的所有帧)
	if len(r.Commands) != 10 {
		t.Fatalf("commands = %d, want 10", len(r.Commands))
	}
	if r.Commands[0].Frame != 11 || r.Commands[9].Frame != 20 {
		t.Fatalf("command range: %d-%d", r.Commands[0].Frame, r.Commands[9].Frame)
	}
	if r.LatestFrame != 20 {
		t.Fatalf("latest = %d", r.LatestFrame)
	}
}

func TestRecover_LongDisconnect_FullReset(t *testing.T) {
	log := cmdlog.NewLog[state, cmd](cmdlog.WithCmdDepth(5))

	log.Checkpoint(1, 10)

	// 记录 5 帧后缓冲满,再记录覆盖旧帧
	for f := uint64(2); f <= 10; f++ {
		log.Record(f, []cmd{"action"})
	}

	// 快照在 f1,但指令缓冲只有 f6-f10(f2-f5 已被覆盖)
	// 从 f1 断线 → 需要 f2-f10 的指令,但 f2-f5 丢了 → FullReset
	r, ok := log.Recover(1)
	if !ok {
		t.Fatal("should succeed")
	}
	if !r.FullReset {
		t.Fatal("should be FullReset due to cmd buffer overflow")
	}
}

func TestRecover_WithRecentSnapshot(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()

	log.Checkpoint(10, 100)
	log.Checkpoint(20, 200)
	log.Checkpoint(30, 300)

	for f := uint64(31); f <= 40; f++ {
		log.Record(f, []cmd{"step"})
	}

	// 断线在 f25,应选 f20 的快照(最近且 <= 25)
	r, ok := log.Recover(25)
	if !ok {
		t.Fatal("should succeed")
	}
	if r.SnapshotFrame != 20 {
		t.Fatalf("snapshot frame = %d, want 20", r.SnapshotFrame)
	}
	if r.Snapshot != 200 {
		t.Fatalf("snapshot = %d, want 200", r.Snapshot)
	}
}

func TestRecover_DisconnectBeforeAnySnapshot(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()

	// 快照在 f50,但玩家断线在 f10(所有快照都在断线之后)
	log.Checkpoint(50, 500)
	for f := uint64(51); f <= 60; f++ {
		log.Record(f, []cmd{"late"})
	}

	r, ok := log.Recover(10)
	if !ok {
		t.Fatal("should succeed with earliest snapshot")
	}
	// 应选最老的快照 f50
	if r.SnapshotFrame != 50 {
		t.Fatalf("snapshot frame = %d", r.SnapshotFrame)
	}
}

func TestRecover_NoSnapshot(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()
	log.Record(1, []cmd{"hello"})

	_, ok := log.Recover(0)
	if ok {
		t.Fatal("no snapshot → should return false")
	}
}

func TestMultipleCheckpoints(t *testing.T) {
	log := cmdlog.NewLog[state, cmd](cmdlog.WithSnapDepth(3))

	log.Checkpoint(10, 100)
	log.Checkpoint(20, 200)
	log.Checkpoint(30, 300)
	log.Checkpoint(40, 400) // 覆盖 f10

	for f := uint64(41); f <= 50; f++ {
		log.Record(f, []cmd{"go"})
	}

	// 断线在 f15 → f10 的快照已被覆盖,应选 f20
	r, ok := log.Recover(15)
	if !ok {
		t.Fatal("should succeed")
	}
	// f10 被覆盖了,最近 <= 15 的没有了,所以会选最老的可用快照
	// 可用快照: f20, f30, f40 (f10 被覆盖)
	// 都 > 15,所以选最老的 f20
	if r.SnapshotFrame != 20 {
		t.Fatalf("snapshot frame = %d, want 20", r.SnapshotFrame)
	}
}

func TestLatestFrame(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()
	if log.LatestFrame() != 0 {
		t.Fatal("initial latest should be 0")
	}
	log.Record(5, nil)
	log.Record(10, nil)
	if log.LatestFrame() != 10 {
		t.Fatalf("latest = %d", log.LatestFrame())
	}
}

func TestCounts(t *testing.T) {
	log := cmdlog.NewLog[state, cmd](cmdlog.WithCmdDepth(4), cmdlog.WithSnapDepth(2))
	if log.CmdCount() != 0 || log.SnapCount() != 0 {
		t.Fatal("initial counts")
	}
	log.Record(1, nil)
	log.Record(2, nil)
	log.Checkpoint(1, 0)
	if log.CmdCount() != 2 {
		t.Fatalf("cmd count = %d", log.CmdCount())
	}
	if log.SnapCount() != 1 {
		t.Fatalf("snap count = %d", log.SnapCount())
	}
}

func TestConcurrentSafe(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()
	var wg sync.WaitGroup
	// 并发写
	for i := range 50 {
		wg.Go(func() {
			f := uint64(i*10 + 1)
			for j := range 10 {
				log.Record(f+uint64(j), []cmd{"action"})
			}
			if i%5 == 0 {
				log.Checkpoint(f, i*100)
			}
		})
	}
	// 并发读
	for range 20 {
		wg.Go(func() {
			log.Recover(50)
			log.LatestFrame()
			log.CmdCount()
		})
	}
	wg.Wait()
}

func TestRecover_EmptyCommands(t *testing.T) {
	log := cmdlog.NewLog[state, cmd]()
	log.Checkpoint(100, 1000)
	// 快照后无指令
	r, ok := log.Recover(50)
	if !ok {
		t.Fatal("should succeed")
	}
	if len(r.Commands) != 0 {
		t.Fatalf("commands should be empty, got %d", len(r.Commands))
	}
	if r.SnapshotFrame != 100 {
		t.Fatalf("snapshot frame = %d", r.SnapshotFrame)
	}
}
