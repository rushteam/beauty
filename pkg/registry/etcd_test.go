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
	_, err = e.Client.Put(context.TODO(), naming+"01", `{"addr":"10.0.0.1","id":"0-0-0-1"}`)
	assert.Equal(t, nil, err)
	e.Client.Put(context.TODO(), naming+"02", `{"addr":"10.0.0.2","id":"0-0-0-2"}`)
	e.Client.Put(context.TODO(), naming+"03", `{"addr":"10.0.0.3","id":"0-0-0-3"}`)
	ctx, cancel := context.WithCancel(context.TODO())
	ch, err := e.Discover(ctx, naming)
	assert.Equal(t, nil, err)
	go func() {
		for {
			select {
			case nodes := <-ch:
				for _, v := range nodes {
					t.Log(v.Address)
				}
			}
		}
	}()
	time.Sleep(1 * time.Second)
	cancel()
	time.Sleep(1 * time.Second)
	t.Fail()
}
