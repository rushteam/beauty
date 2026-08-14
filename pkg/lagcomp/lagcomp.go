// Package lagcomp 在 snapbuf + inputclock 之上提供 server rewind 查询(延迟补偿)。
package lagcomp

import (
	"time"

	"github.com/rushteam/beauty/pkg/inputclock"
	"github.com/rushteam/beauty/pkg/snapbuf"
)

// Compensator 根据 shooter 的 clientFrame 查询补偿后的世界快照。
type Compensator[S any] struct {
	Clock *inputclock.Clock
	Ring  *snapbuf.Ring[S]
	Tick  time.Duration
}

// WorldAt 返回 shooter 在 clientFrame 射击时刻应使用的补偿快照。
func (c *Compensator[S]) WorldAt(shooter string, clientFrame uint64) (S, uint64, bool) {
	if c == nil || c.Clock == nil || c.Ring == nil {
		var zero S
		return zero, 0, false
	}
	tick := c.Tick
	if tick <= 0 {
		tick = 50 * time.Millisecond
	}
	target, ok := c.Clock.CompensatedFrame(shooter, clientFrame, tick)
	if !ok {
		var zero S
		return zero, 0, false
	}
	snap, frame, ok := c.Ring.Nearest(target)
	return snap, frame, ok
}

// ExactAt 不做 RTT 补偿,精确查 target 帧(测试/回放)。
func (c *Compensator[S]) ExactAt(frame uint64) (S, bool) {
	if c == nil || c.Ring == nil {
		var zero S
		return zero, false
	}
	return c.Ring.At(frame)
}
