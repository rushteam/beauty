package xgo

import (
	"fmt"
	"testing"
)

func TestMain(t *testing.T) {
	p := New()
	p.Go(func() {
		fmt.Println(1)
	})
	p.Go(func() {
		fmt.Println(1)
	})
	p.Go(func() {
		fmt.Println(1)
	})
	t.Fail()
}
