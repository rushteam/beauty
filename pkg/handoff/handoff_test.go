package handoff_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/handoff"
)

// 简单实体:角色位置 + 背包
type playerState struct {
	Name string
	X, Y int
	Gold int
}

// 操作:移动/加金
type playerCmd struct {
	Type string // "move" / "gold"
	DX   int
	DY   int
	Gold int
}

func applyCmd(s playerState, c playerCmd) playerState {
	switch c.Type {
	case "move":
		s.X += c.DX
		s.Y += c.DY
	case "gold":
		s.Gold += c.Gold
	}
	return s
}

func TestFullMigrationFlow(t *testing.T) {
	initial := playerState{Name: "hero", X: 100, Y: 200, Gold: 500}
	src := handoff.NewSource[playerState, playerCmd](initial)

	if src.Phase() != handoff.PhaseOwned {
		t.Fatalf("initial phase = %v", src.Phase())
	}

	// 正常操作:Buffer 返回 false(不缓冲,本地执行)
	buffered, err := src.Buffer(playerCmd{Type: "move", DX: 1})
	if err != nil || buffered {
		t.Fatal("should not buffer in Owned phase")
	}

	// Step 1: Begin 导出
	if err := src.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if src.Phase() != handoff.PhaseExporting {
		t.Fatal("should be Exporting")
	}

	// 导出期间:操作被缓冲
	src.Buffer(playerCmd{Type: "move", DX: 5, DY: 3})
	src.Buffer(playerCmd{Type: "gold", Gold: 100})

	// Step 2: Export 导出迁移包
	bundle, err := src.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if bundle.State.Name != "hero" || bundle.State.X != 100 {
		t.Fatalf("state = %+v", bundle.State)
	}
	if len(bundle.Buffered) != 2 {
		t.Fatalf("buffered = %d", len(bundle.Buffered))
	}

	// Step 3: Target Import
	tgt := handoff.NewTarget[playerState, playerCmd]()
	if tgt.Phase() != handoff.PhaseImporting {
		t.Fatal("target should start as Importing")
	}

	cmds, err := tgt.Import(bundle)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// 重放缓冲操作
	state := bundle.State
	for _, c := range cmds {
		state = applyCmd(state, c)
	}
	tgt.UpdateState(state)

	// Step 4: Accept 确认接管
	if err := tgt.Accept(); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if tgt.Phase() != handoff.PhaseOwned {
		t.Fatal("target should be Owned")
	}

	// 验证最终状态
	final := tgt.State()
	if final.X != 105 || final.Y != 203 || final.Gold != 600 {
		t.Fatalf("final state = %+v", final)
	}

	// Step 5: Source Release
	if err := src.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if src.Phase() != handoff.PhaseReleased {
		t.Fatal("source should be Released")
	}

	// Released 后操作返回 ErrReleased
	_, err = src.Buffer(playerCmd{Type: "move"})
	if !errors.Is(err, handoff.ErrReleased) {
		t.Fatalf("want ErrReleased, got %v", err)
	}
}

func TestAbort_ReturnsBuffered(t *testing.T) {
	src := handoff.NewSource[playerState, playerCmd](playerState{Name: "hero"})
	src.Begin()
	src.Buffer(playerCmd{Type: "move", DX: 1})
	src.Buffer(playerCmd{Type: "move", DX: 2})

	cmds, err := src.Abort()
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("returned cmds = %d", len(cmds))
	}
	if src.Phase() != handoff.PhaseOwned {
		t.Fatal("should be back to Owned")
	}

	// Abort 后可以重新 Begin
	if err := src.Begin(); err != nil {
		t.Fatalf("re-Begin: %v", err)
	}
}

func TestDrainBuffer_IncrementalCatchUp(t *testing.T) {
	src := handoff.NewSource[playerState, playerCmd](playerState{})
	src.Begin()
	src.Buffer(playerCmd{Type: "move", DX: 1})

	// 第一次 Export
	bundle, _ := src.Export()
	if len(bundle.Buffered) != 1 {
		t.Fatal("first export should have 1 cmd")
	}

	// 传输期间又有新操作
	src.Buffer(playerCmd{Type: "gold", Gold: 50})
	src.Buffer(playerCmd{Type: "move", DX: 3})

	// DrainBuffer 增量取走
	extra, err := src.DrainBuffer()
	if err != nil {
		t.Fatalf("DrainBuffer: %v", err)
	}
	if len(extra) != 2 {
		t.Fatalf("extra = %d", len(extra))
	}

	// 再次 drain 应为空
	extra2, _ := src.DrainBuffer()
	if len(extra2) != 0 {
		t.Fatal("second drain should be empty")
	}
}

func TestDoubleBegin_Error(t *testing.T) {
	src := handoff.NewSource[playerState, playerCmd](playerState{})
	src.Begin()
	if err := src.Begin(); !errors.Is(err, handoff.ErrAlreadyExport) {
		t.Fatalf("want ErrAlreadyExport, got %v", err)
	}
}

func TestExportNotExporting_Error(t *testing.T) {
	src := handoff.NewSource[playerState, playerCmd](playerState{})
	_, err := src.Export()
	if !errors.Is(err, handoff.ErrNotExporting) {
		t.Fatalf("want ErrNotExporting, got %v", err)
	}
}

func TestTargetImportNotImporting_Error(t *testing.T) {
	tgt := handoff.NewTarget[playerState, playerCmd]()
	tgt.Import(handoff.Bundle[playerState, playerCmd]{})
	tgt.Accept()
	_, err := tgt.Import(handoff.Bundle[playerState, playerCmd]{})
	if !errors.Is(err, handoff.ErrNotImporting) {
		t.Fatalf("want ErrNotImporting, got %v", err)
	}
}

func TestConcurrentBuffer(t *testing.T) {
	src := handoff.NewSource[int, int](0)
	src.Begin()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			src.Buffer(i)
		})
	}
	wg.Wait()

	bundle, _ := src.Export()
	if len(bundle.Buffered) != 100 {
		t.Fatalf("buffered = %d, want 100", len(bundle.Buffered))
	}
}

func TestPhase_String(t *testing.T) {
	phases := []struct {
		p    handoff.Phase
		want string
	}{
		{handoff.PhaseOwned, "owned"},
		{handoff.PhaseExporting, "exporting"},
		{handoff.PhaseReleased, "released"},
		{handoff.PhaseImporting, "importing"},
	}
	for _, c := range phases {
		if got := c.p.String(); got != c.want {
			t.Fatalf("%d.String() = %s, want %s", c.p, got, c.want)
		}
	}
}
