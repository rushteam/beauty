package lifecycle

import (
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

//Cycle ..
type Cycle struct {
	mu uint32
	sync.Once
	eg   *errgroup.Group
	quit chan struct{}
}

//NewCycle new a cycle life
func NewCycle() *Cycle {
	return &Cycle{
		mu:   0,
		eg:   &errgroup.Group{},
		quit: make(chan struct{}),
	}
}

//Run a new goroutine
func (c *Cycle) Run(fn func() error) {
	c.eg.Go(fn)
}

//Done block and return a chan error
func (c *Cycle) Done() <-chan error {
	errCh := make(chan error)
	go func() {
		if err := c.eg.Wait(); err != nil {
			errCh <- err
		}
		close(errCh)
	}()
	return errCh
}

//DoneAndClose ..
func (c *Cycle) DoneAndClose() {
	<-c.Done()
	c.Close()
}

//Close ..
func (c *Cycle) Close() {
	if c.mu == 0 {
		if atomic.CompareAndSwapUint32(&c.mu, 0, 1) {
			close(c.quit)
		}
	}
}

// Wait blocked for a life cycle
func (c *Cycle) Wait() <-chan struct{} {
	return c.quit
}
