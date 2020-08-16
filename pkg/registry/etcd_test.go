package registry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDiscover(t *testing.T) {
	naming := "/beauty_test/service/"
	e, err := Build()
	assert.Equal(t, nil, err)
	e.Client.Put(context.TODO(), naming+"01", "-")
	ctx, cancel := context.WithCancel(context.TODO())
	ch, err := e.Discover(ctx, naming)
	assert.Equal(t, nil, err)
	go func() {
		for {
			nodes := <-ch
			t.Log(nodes)
		}
	}()
	time.Sleep(1 * time.Second)
	cancel()
	time.Sleep(1 * time.Second)
	t.Fail()
}
