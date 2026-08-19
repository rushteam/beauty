package fieldmask_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/fieldmask"
)

const (
	fieldHP    = 0
	fieldMP    = 1
	fieldPosX  = 2
	fieldPosY  = 3
	fieldBuff  = 10
	fieldEquip = 20
)

func TestSetField_SingleField(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero1", fieldHP)

	if !tr.IsDirty("hero1") {
		t.Fatal("hero1 should be dirty")
	}
	if !tr.IsFieldDirty("hero1", fieldHP) {
		t.Fatal("HP should be dirty")
	}
	if tr.IsFieldDirty("hero1", fieldMP) {
		t.Fatal("MP should not be dirty")
	}
}

func TestSetFields_Multiple(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetFields("hero1", fieldHP, fieldPosX, fieldBuff)

	if !tr.IsFieldDirty("hero1", fieldHP) {
		t.Fatal("HP")
	}
	if !tr.IsFieldDirty("hero1", fieldPosX) {
		t.Fatal("PosX")
	}
	if !tr.IsFieldDirty("hero1", fieldBuff) {
		t.Fatal("Buff")
	}
	if tr.IsFieldDirty("hero1", fieldMP) {
		t.Fatal("MP should not be dirty")
	}
}

func TestFlush_ReturnsPatches(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero1", fieldHP)
	tr.SetField("hero1", fieldMP)
	tr.SetField("hero2", fieldPosX)

	patches := tr.Flush()
	if len(patches) != 2 {
		t.Fatalf("patches = %d, want 2", len(patches))
	}

	patchMap := make(map[string]fieldmask.Patch[string])
	for _, p := range patches {
		patchMap[p.Key] = p
	}

	h1 := patchMap["hero1"]
	slices.Sort(h1.Fields)
	if len(h1.Fields) != 2 || h1.Fields[0] != fieldHP || h1.Fields[1] != fieldMP {
		t.Fatalf("hero1 fields = %v, want [%d,%d]", h1.Fields, fieldHP, fieldMP)
	}

	h2 := patchMap["hero2"]
	if len(h2.Fields) != 1 || h2.Fields[0] != fieldPosX {
		t.Fatalf("hero2 fields = %v", h2.Fields)
	}
}

func TestFlush_ClearsDirty(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero1", fieldHP)

	tr.Flush()

	if tr.IsDirty("hero1") {
		t.Fatal("should be clean after Flush")
	}
	patches := tr.Flush()
	if len(patches) != 0 {
		t.Fatal("second Flush should be empty")
	}
}

func TestFlushKey(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero1", fieldHP)
	tr.SetField("hero2", fieldMP)

	p := tr.FlushKey("hero1")
	if p == nil || len(p.Fields) != 1 || p.Fields[0] != fieldHP {
		t.Fatalf("FlushKey hero1 = %+v", p)
	}

	// hero1 已清,hero2 仍脏
	if tr.IsDirty("hero1") {
		t.Fatal("hero1 should be clean")
	}
	if !tr.IsDirty("hero2") {
		t.Fatal("hero2 should still be dirty")
	}
}

func TestVersion_IncrementOnSet(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	if v := tr.Version("hero1"); v != 0 {
		t.Fatalf("initial version = %d", v)
	}

	tr.SetField("hero1", fieldHP)
	if v := tr.Version("hero1"); v != 1 {
		t.Fatalf("version after 1 set = %d", v)
	}

	tr.SetFields("hero1", fieldMP, fieldPosX)
	if v := tr.Version("hero1"); v != 2 {
		t.Fatalf("version after SetFields = %d", v)
	}
}

func TestVersion_SurvivesFlush(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero1", fieldHP)
	tr.SetField("hero1", fieldMP)
	tr.Flush()

	// 版本号不因 Flush 重置
	if v := tr.Version("hero1"); v != 2 {
		t.Fatalf("version after Flush = %d, want 2", v)
	}
}

func TestRemove(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero1", fieldHP)
	tr.Remove("hero1")

	if tr.IsDirty("hero1") {
		t.Fatal("removed entity should not be dirty")
	}
	if v := tr.Version("hero1"); v != 0 {
		t.Fatalf("version after Remove = %d", v)
	}
}

func TestNotDirty_UnknownEntity(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	if tr.IsDirty("nobody") {
		t.Fatal("unknown entity should not be dirty")
	}
	if tr.IsFieldDirty("nobody", 0) {
		t.Fatal("unknown entity field should not be dirty")
	}
}

func TestLen(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	if tr.Len() != 0 {
		t.Fatal("empty tracker Len != 0")
	}
	tr.SetField("a", 0)
	tr.SetField("b", 1)
	if tr.Len() != 2 {
		t.Fatalf("Len = %d", tr.Len())
	}
	tr.Flush()
	if tr.Len() != 0 {
		t.Fatalf("Len after Flush = %d", tr.Len())
	}
}

func TestConcurrentSafe(t *testing.T) {
	tr := fieldmask.NewTracker[int]()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			for j := range 50 {
				tr.SetField(i, j)
				tr.IsFieldDirty(i, j)
				tr.Version(i)
			}
		})
	}
	wg.Wait()
	patches := tr.Flush()
	if len(patches) != 100 {
		t.Fatalf("patches = %d, want 100", len(patches))
	}
}

func TestHighFieldID(t *testing.T) {
	tr := fieldmask.NewTracker[string]()
	tr.SetField("hero", 500)
	if !tr.IsFieldDirty("hero", 500) {
		t.Fatal("field 500 should be dirty")
	}
	p := tr.Flush()
	if len(p) != 1 || p[0].Fields[0] != 500 {
		t.Fatalf("unexpected patch: %+v", p)
	}
}
