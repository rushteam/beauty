package lifecycle

import (
	"sync"
	"sync/atomic"
)

//Cycle ..
type Cycle struct {
	mu uint32
	sync.Once
	wg   *sync.WaitGroup
	quit chan error
}

//NewCycle new a cycle life
func NewCycle() *Cycle {
	return &Cycle{
		mu:   0,
		wg:   &sync.WaitGroup{},
		quit: make(chan error),
	}
}

//Run a new goroutine
func (c *Cycle) Run(fn func() error) {
	c.wg.Add(1)
	go func(c *Cycle) {
		defer c.wg.Done()
		if err := fn(); err != nil {
			c.quit <- err
		}
	}(c)

}

//Done block and return a chan error
func (c *Cycle) Done() <-chan error {
	go func() {
		c.wg.Wait()
		c.Close()
	}()
	return c.quit
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
func (c *Cycle) Wait() <-chan error {
	return c.quit
}
