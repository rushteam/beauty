package snapbuf_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/game/snapbuf"
)

func TestRingAtAndNearest(t *testing.T) {
	r := snapbuf.New[int](4)
	r.Push(1, 10)
	r.Push(2, 20)
	r.Push(3, 30)

	v, ok := r.At(2)
	if !ok || v != 20 {
		t.Fatalf("At(2)=%v ok=%v", v, ok)
	}
	v, frame, ok := r.Nearest(2)
	if !ok || v != 20 || frame != 2 {
		t.Fatalf("Nearest(2)=%v frame=%d", v, frame)
	}
	v, frame, ok = r.Nearest(99)
	if !ok || v != 30 || frame != 3 {
		t.Fatalf("Nearest(99)=%v frame=%d", v, frame)
	}
}
