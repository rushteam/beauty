package lagcomp_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/inputclock"
	"github.com/rushteam/beauty/pkg/lagcomp"
	"github.com/rushteam/beauty/pkg/snapbuf"
)

func TestWorldAt(t *testing.T) {
	ring := snapbuf.New[int](8)
	ring.Push(98, 980)
	ring.Push(99, 990)
	ring.Push(100, 1000)

	clock := inputclock.New()
	clock.Record(inputclock.Sample{Player: "p1", ClientFrame: 5, ServerFrame: 100})
	clock.UpdateRTT("p1", 100*time.Millisecond)

	c := &lagcomp.Compensator[int]{
		Clock: clock,
		Ring:  ring,
		Tick:  50 * time.Millisecond,
	}
	v, frame, ok := c.WorldAt("p1", 5)
	if !ok || v != 980 || frame != 98 {
		t.Fatalf("WorldAt = %v frame=%d ok=%v", v, frame, ok)
	}
}
