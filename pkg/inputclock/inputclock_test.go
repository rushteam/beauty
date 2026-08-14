package inputclock_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/inputclock"
)

func TestCompensatedFrame(t *testing.T) {
	c := inputclock.New()
	c.Record(inputclock.Sample{Player: "p1", ClientFrame: 10, ServerFrame: 100})
	c.UpdateRTT("p1", 100*time.Millisecond)

	tick := 50 * time.Millisecond
	frame, ok := c.CompensatedFrame("p1", 10, tick)
	if !ok {
		t.Fatal("expected ok")
	}
	if frame != 98 {
		t.Fatalf("compensated frame = %d want 98", frame)
	}
}
