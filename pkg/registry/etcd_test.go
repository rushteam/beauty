package registry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiscover(t *testing.T) {
	naming := "/beauty_test/service/"
	e, err := Build()
	assert.Equal(t, nil, err)
	e.Client.Put(context.TODO(), naming+"01", "-")
	e.Discover(context.TODO(), naming)
}
