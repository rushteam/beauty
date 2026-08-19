package interpolate

import (
	"math"
	"testing"
	"time"
)

func TestBufferPushAndLen(t *testing.T) {
	buf := NewBuffer(8)
	buf.Push(Frame{ServerTime: 100 * time.Millisecond, Entities: []Snapshot{{ID: "a", X: 1}}})
	buf.Push(Frame{ServerTime: 150 * time.Millisecond, Entities: []Snapshot{{ID: "a", X: 2}}})
	buf.Push(Frame{ServerTime: 200 * time.Millisecond, Entities: []Snapshot{{ID: "a", X: 3}}})
	if buf.Len() != 3 {
		t.Fatalf("Len = %d, want 3", buf.Len())
	}
}

func TestBufferOutOfOrder(t *testing.T) {
	buf := NewBuffer(8)
	buf.Push(Frame{ServerTime: 200 * time.Millisecond})
	buf.Push(Frame{ServerTime: 100 * time.Millisecond}) // 乱序
	buf.Push(Frame{ServerTime: 150 * time.Millisecond}) // 乱序

	before, after, _, ok := buf.Bracket(125 * time.Millisecond)
	if !ok {
		t.Fatal("Bracket failed")
	}
	if before.ServerTime != 100*time.Millisecond || after.ServerTime != 150*time.Millisecond {
		t.Errorf("Bracket = [%v, %v], want [100ms, 150ms]", before.ServerTime, after.ServerTime)
	}
}

func TestBufferCapacity(t *testing.T) {
	buf := NewBuffer(4)
	for i := range 10 {
		buf.Push(Frame{ServerTime: time.Duration(i) * 50 * time.Millisecond})
	}
	if buf.Len() != 4 {
		t.Errorf("Len = %d after overflow, want 4", buf.Len())
	}
}

func TestBracketInterpolation(t *testing.T) {
	buf := NewBuffer(8)
	buf.Push(Frame{ServerTime: 100 * time.Millisecond, Entities: []Snapshot{{ID: "a", X: 0, Y: 0}}})
	buf.Push(Frame{ServerTime: 200 * time.Millisecond, Entities: []Snapshot{{ID: "a", X: 10, Y: 20}}})

	before, after, frac, ok := buf.Bracket(150 * time.Millisecond)
	if !ok {
		t.Fatal("Bracket returned !ok")
	}
	if before.ServerTime != 100*time.Millisecond {
		t.Errorf("before.ServerTime = %v, want 100ms", before.ServerTime)
	}
	if after.ServerTime != 200*time.Millisecond {
		t.Errorf("after.ServerTime = %v, want 200ms", after.ServerTime)
	}
	if math.Abs(frac-0.5) > 1e-9 {
		t.Errorf("t = %v, want 0.5", frac)
	}
}

func TestBracketExtrapolation(t *testing.T) {
	buf := NewBuffer(8)
	buf.Push(Frame{ServerTime: 100 * time.Millisecond})
	buf.Push(Frame{ServerTime: 200 * time.Millisecond})

	_, _, frac, ok := buf.Bracket(250 * time.Millisecond)
	if !ok {
		t.Fatal("Bracket should succeed for extrapolation")
	}
	if math.Abs(frac-1.5) > 1e-9 {
		t.Errorf("extrapolation t = %v, want 1.5", frac)
	}
}

func TestLerpSnapshot(t *testing.T) {
	a := Snapshot{ID: "p1", X: 0, Y: 0, Angle: 350}
	b := Snapshot{ID: "p1", X: 10, Y: 20, Angle: 10}

	mid := LerpSnapshot(a, b, 0.5)
	if math.Abs(mid.X-5) > 1e-9 || math.Abs(mid.Y-10) > 1e-9 {
		t.Errorf("LerpSnapshot pos = (%v,%v), want (5,10)", mid.X, mid.Y)
	}
	// 角度: 350→10 应该走 +20 的短弧, 中点=0(即360)
	if math.Abs(mid.Angle-0) > 1e-9 && math.Abs(mid.Angle-360) > 1e-9 {
		t.Errorf("LerpSnapshot angle = %v, want 0 or 360", mid.Angle)
	}
}

func TestHermiteSnapshot(t *testing.T) {
	a := Snapshot{ID: "p1", X: 0, Y: 0, VX: 10, VY: 0}
	b := Snapshot{ID: "p1", X: 10, Y: 0, VX: 10, VY: 0}

	// 匀速直线运动,Hermite 在 t=0.5 应接近 x=5
	mid := HermiteSnapshot(a, b, 0.5, 1.0)
	if math.Abs(mid.X-5) > 0.5 {
		t.Errorf("HermiteSnapshot X = %v, want ~5", mid.X)
	}
}

func TestInterpolateFrame(t *testing.T) {
	before := Frame{
		Entities: []Snapshot{
			{ID: "a", X: 0, Y: 0},
			{ID: "b", X: 10, Y: 10},
		},
	}
	after := Frame{
		Entities: []Snapshot{
			{ID: "a", X: 10, Y: 20},
			{ID: "b", X: 20, Y: 30},
			{ID: "c", X: 5, Y: 5}, // 新出现的实体
		},
	}
	result := InterpolateFrame(before, after, 0.5)
	if len(result) != 3 {
		t.Fatalf("InterpolateFrame returned %d entities, want 3", len(result))
	}
	m := map[string]Snapshot{}
	for _, s := range result {
		m[s.ID] = s
	}
	if math.Abs(m["a"].X-5) > 1e-9 || math.Abs(m["a"].Y-10) > 1e-9 {
		t.Errorf("entity a = (%v,%v), want (5,10)", m["a"].X, m["a"].Y)
	}
	if m["c"].X != 5 {
		t.Errorf("new entity c should be at X=5, got %v", m["c"].X)
	}
}

func TestTimeLine(t *testing.T) {
	localTime := time.Now()
	clock := func() time.Time { return localTime }

	tl := NewTimeLine(WithRenderDelay(100*time.Millisecond), WithNow(clock))

	// 模拟:本地过去 200ms 时收到 serverTime=200ms 的帧(offset=0)
	localTime = localTime.Add(200 * time.Millisecond)
	tl.OnServerFrame(200 * time.Millisecond)

	// 本地过去 300ms 时查询渲染时间
	localTime = localTime.Add(100 * time.Millisecond)
	rt := tl.RenderTime()
	// 期望: localElapsed=300ms, offset≈0, renderDelay=100ms → renderTime ≈ 200ms
	expected := 200 * time.Millisecond
	if abs(rt-expected) > 5*time.Millisecond {
		t.Errorf("RenderTime = %v, want ~%v", rt, expected)
	}
}

func TestTimeLineJitterAbsorption(t *testing.T) {
	localTime := time.Now()
	clock := func() time.Time { return localTime }
	tl := NewTimeLine(WithRenderDelay(100*time.Millisecond), WithNow(clock))

	// 正常帧: localElapsed=100ms, serverTime=100ms → offset=0
	localTime = localTime.Add(100 * time.Millisecond)
	tl.OnServerFrame(100 * time.Millisecond)

	// 抖动帧: localElapsed=200ms, serverTime=150ms → newOffset=50ms (网络延迟突增)
	localTime = localTime.Add(100 * time.Millisecond)
	tl.OnServerFrame(150 * time.Millisecond)

	// EMA 应该只部分追踪,不会完全跳变
	localTime = localTime.Add(50 * time.Millisecond)
	rt := tl.RenderTime()
	// offset 应在 0~50ms 之间(被 EMA 平滑)
	// renderTime = 250ms - offset - 100ms
	if rt < 100*time.Millisecond || rt > 150*time.Millisecond {
		t.Errorf("RenderTime after jitter = %v, expect between 100ms and 150ms", rt)
	}
}

func TestLerpAngleWrap(t *testing.T) {
	cases := []struct{ a, b, t, want float64 }{
		{10, 350, 0.5, 0}, // 应走短弧 10→0→350
		{350, 10, 0.5, 0}, // 同上反方向
		{0, 180, 0.5, 90}, // 半圈
		{90, 90, 0.5, 90}, // 不动
	}
	for i, c := range cases {
		got := lerpAngle(c.a, c.b, c.t)
		// 归一化到 [0, 360) 比较
		got = math.Mod(got+360, 360)
		want := math.Mod(c.want+360, 360)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("case %d: lerpAngle(%v,%v,%v) = %v, want %v", i, c.a, c.b, c.t, got, want)
		}
	}
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
