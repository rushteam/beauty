package xgo

import (
	"container/list"
	"math"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/rushteam/beauty/pkg/logger"
)

type Pool interface {
	Go(f func())
	Workers() int32
}

type pool struct {
	name           string
	cap            int32
	scaleThreshold int
	workerNum      int32

	taskLock sync.Mutex
	tasks    *list.List

	panicHandler func(any)
}

func WithSetCap(cap int32) Option {
	return func(p *pool) {
		p.cap = cap
	}
}
func WithPanicHandler(f func(any)) Option {
	return func(p *pool) {
		p.panicHandler = f
	}
}

type Option func(p *pool)

func New(opts ...Option) Pool {
	p := &pool{
		cap:            math.MaxInt32,
		scaleThreshold: 1,
		tasks:          new(list.List),
	}
	p.panicHandler = func(r any) {
		logger.Error("XGO: panic in pool: %s: %v: %s", p.name, r, debug.Stack())
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *pool) Go(f func()) {
	p.addTask(f)
	if (p.taskNum() >= p.scaleThreshold && p.Workers() < atomic.LoadInt32(&p.cap)) || p.Workers() == 0 {
		p.run()
	}
}

func (p *pool) addTask(f func()) {
	p.taskLock.Lock()
	defer p.taskLock.Unlock()
	p.tasks.PushBack(f)
}

func (p *pool) taskNum() int {
	return p.tasks.Len()
}

func (p *pool) popTask() func() {
	p.taskLock.Lock()
	defer p.taskLock.Unlock()
	el := p.tasks.Front()
	if el == nil {
		return nil
	}
	p.tasks.Remove(el)
	return el.Value.(func())
}

func (p *pool) run() {
	atomic.AddInt32(&p.workerNum, 1)
	go func() {
		for {
			var f = p.popTask()
			if f == nil {
				// empty task
				atomic.AddInt32(&p.workerNum, -1)
				return
			}
			func() {
				defer func() {
					if r := recover(); r != nil && p.panicHandler != nil {
						p.panicHandler(r)
					}
				}()
				f()
			}()
		}
	}()
}

func (p *pool) Workers() int32 {
	return atomic.LoadInt32(&p.workerNum)
}
