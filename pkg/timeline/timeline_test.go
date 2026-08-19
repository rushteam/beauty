package timeline_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/timeline"
)

// 战斗事件
type battleEvent struct {
	Type   string
	Target string
	Value  int
}

func hit(target string, dmg int) battleEvent {
	return battleEvent{Type: "hit", Target: target, Value: dmg}
}

func death(target string) battleEvent {
	return battleEvent{Type: "death", Target: target}
}

func victory() battleEvent {
	return battleEvent{Type: "victory"}
}

func vfx(name string) battleEvent {
	return battleEvent{Type: "vfx", Target: name}
}

func TestAppend_And_Snapshot(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	tl.Append(10, hit("goblin", 50))
	tl.Append(0, vfx("slash"))
	tl.Append(5, hit("orc", 30))

	snap := tl.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snap len = %d", len(snap))
	}
	// 按 offset 升序
	if snap[0].Offset != 0 || snap[0].Event.Type != "vfx" {
		t.Fatalf("snap[0] = %+v", snap[0])
	}
	if snap[1].Offset != 5 {
		t.Fatalf("snap[1].Offset = %d", snap[1].Offset)
	}
	if snap[2].Offset != 10 {
		t.Fatalf("snap[2].Offset = %d", snap[2].Offset)
	}
}

func TestAfter_ChainedTiming(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	tl.After(0, vfx("cast_start")) // offset=0
	tl.After(30, hit("boss", 500)) // offset=30
	tl.After(20, death("boss"))    // offset=50
	tl.After(10, victory())        // offset=60

	snap := tl.Snapshot()
	if len(snap) != 4 {
		t.Fatalf("len = %d", len(snap))
	}
	offsets := []int{0, 30, 50, 60}
	for i, e := range snap {
		if e.Offset != offsets[i] {
			t.Fatalf("snap[%d].Offset = %d, want %d", i, e.Offset, offsets[i])
		}
	}

	if tl.Duration() != 60 {
		t.Fatalf("Duration = %d", tl.Duration())
	}
}

func TestStagger_MultiHit(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	tl.After(0, vfx("arrow_rain"))
	tl.Stagger(5, hit("a", 10), hit("b", 10), hit("c", 10))
	// stagger 不推进 cursor,下一个 After 从 cursor=0 继续
	tl.After(20, vfx("rain_end"))

	snap := tl.Snapshot()
	// vfx(0), hit_a(0), hit_b(5), hit_c(10), rain_end(20)
	if len(snap) != 5 {
		t.Fatalf("len = %d", len(snap))
	}
	wantOffsets := []int{0, 0, 5, 10, 20}
	for i, e := range snap {
		if e.Offset != wantOffsets[i] {
			t.Fatalf("snap[%d].Offset = %d, want %d", i, e.Offset, wantOffsets[i])
		}
	}
}

func TestPlayer_FrameByFrame(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	tl.At(1, vfx("slash"))
	tl.At(3, hit("goblin", 50))
	tl.At(3, vfx("blood"))
	tl.At(5, death("goblin"))

	p := timeline.NewPlayer[battleEvent](&tl)

	// tick 1: slash
	ev := p.Advance()
	if len(ev) != 1 || ev[0].Type != "vfx" {
		t.Fatalf("tick 1: %+v", ev)
	}

	// tick 2: 无事件
	ev = p.Advance()
	if len(ev) != 0 {
		t.Fatalf("tick 2: %+v", ev)
	}

	// tick 3: hit + blood
	ev = p.Advance()
	if len(ev) != 2 {
		t.Fatalf("tick 3: len = %d", len(ev))
	}

	// tick 4: 无
	ev = p.Advance()
	if len(ev) != 0 {
		t.Fatalf("tick 4: %+v", ev)
	}

	// tick 5: death
	ev = p.Advance()
	if len(ev) != 1 || ev[0].Type != "death" {
		t.Fatalf("tick 5: %+v", ev)
	}

	if !p.Done() {
		t.Fatal("should be done")
	}
}

func TestPlayer_AdvanceTo_SkipFrames(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	tl.At(1, hit("a", 10))
	tl.At(5, hit("b", 20))
	tl.At(10, hit("c", 30))

	p := timeline.NewPlayer[battleEvent](&tl)

	// 跳到 tick 5:应收到 tick 1 和 tick 5 的事件
	ev := p.AdvanceTo(5)
	if len(ev) != 2 {
		t.Fatalf("AdvanceTo(5): len = %d", len(ev))
	}
	if p.Tick() != 5 {
		t.Fatalf("tick = %d", p.Tick())
	}

	// 不回退
	ev = p.AdvanceTo(3)
	if len(ev) != 0 {
		t.Fatal("backward should return nothing")
	}

	// 跳到结尾
	ev = p.AdvanceTo(10)
	if len(ev) != 1 || ev[0].Target != "c" {
		t.Fatalf("AdvanceTo(10): %+v", ev)
	}
	if !p.Done() {
		t.Fatal("should be done")
	}
}

func TestPlayer_Remaining(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	tl.At(1, hit("a", 10))
	tl.At(2, hit("b", 20))
	tl.At(3, hit("c", 30))

	p := timeline.NewPlayer[battleEvent](&tl)
	if p.Remaining() != 3 {
		t.Fatalf("remaining = %d", p.Remaining())
	}
	p.Advance() // tick 1: hit a
	if p.Remaining() != 2 {
		t.Fatalf("remaining = %d", p.Remaining())
	}
}

func TestTimeline_EmptyDuration(t *testing.T) {
	var tl timeline.Timeline[battleEvent]
	if tl.Duration() != 0 {
		t.Fatal("empty duration should be 0")
	}
	if tl.Len() != 0 {
		t.Fatal("empty len should be 0")
	}
}

func TestTimeline_ZeroValue(t *testing.T) {
	var tl timeline.Timeline[int]
	tl.Append(0, 42)
	snap := tl.Snapshot()
	if len(snap) != 1 || snap[0].Event != 42 {
		t.Fatalf("zero value timeline: %+v", snap)
	}
}

func TestPlayer_FromEntries(t *testing.T) {
	entries := []timeline.Entry[string]{
		{Offset: 1, Event: "hello"},
		{Offset: 3, Event: "world"},
	}
	p := timeline.NewPlayerFromEntries(entries)
	ev := p.Advance()
	if len(ev) != 1 || ev[0] != "hello" {
		t.Fatalf("tick 1: %v", ev)
	}
}

// 完整战斗时间线:模拟回合制战斗
func TestFullBattleTimeline(t *testing.T) {
	var tl timeline.Timeline[battleEvent]

	// 回合 1:英雄施法 → 命中 → 受击特效
	tl.After(0, vfx("hero_cast"))  // 0
	tl.After(20, hit("boss", 200)) // 20
	tl.After(10, vfx("boss_hurt")) // 30

	// 回合 2:BOSS 反击
	tl.After(15, vfx("boss_attack")) // 45
	tl.After(20, hit("hero", 150))   // 65

	// 回合 3:英雄终结
	tl.After(10, vfx("hero_ultimate")) // 75
	tl.After(30, hit("boss", 9999))    // 105
	tl.After(5, death("boss"))         // 110
	tl.After(20, victory())            // 130

	if tl.Len() != 9 {
		t.Fatalf("len = %d", tl.Len())
	}
	if tl.Duration() != 130 {
		t.Fatalf("duration = %d", tl.Duration())
	}

	// 播放器逐帧推进,验证事件按时序到达
	p := timeline.NewPlayer[battleEvent](&tl)
	eventLog := make([]string, 0)
	for !p.Done() {
		events := p.Advance()
		for _, e := range events {
			eventLog = append(eventLog, e.Type)
		}
	}

	if len(eventLog) != 9 {
		t.Fatalf("event log = %v", eventLog)
	}
	// 首个应是 hero_cast(vfx),最后应是 victory
	if eventLog[0] != "vfx" {
		t.Fatalf("first event = %s", eventLog[0])
	}
	if eventLog[len(eventLog)-1] != "victory" {
		t.Fatalf("last event = %s", eventLog[len(eventLog)-1])
	}
}
