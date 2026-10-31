package syncx_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/foundation/syncx"
)

func TestGroupDoChan(t *testing.T) {
	var g syncx.Group[int]
	ch := g.DoChan("k", func() (int, error) {
		time.Sleep(10 * time.Millisecond)
		return 7, nil
	})
	select {
	case r := <-ch:
		if r.Err != nil || r.Val != 7 {
			t.Fatalf("result=%+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
