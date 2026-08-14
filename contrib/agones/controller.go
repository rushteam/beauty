// Package agones 把 Agones GameServer 生命周期与 pkg/gameroom 对齐。
// 独立模块——依赖 agones.dev SDK;本地开发可注入 Lifecycle mock。
package agones

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/gameroom"
)

// Lifecycle 抽象 Agones SDK(便于测试与替换)。
type Lifecycle interface {
	Ready() error
	Shutdown() error
	Health() error
}

// Controller 监听 Agones 信号并驱动 gameroom.Manager。
type Controller struct {
	SDK     Lifecycle
	Manager *gameroom.Manager
	RoomID  string

	// ShutdownGrace 收到 Shutdown 后等待对局结束的最长时间(默认 30s)。
	ShutdownGrace time.Duration

	mu      sync.Mutex
	running bool
}

// Option 配置 Controller。
type Option func(*Controller)

// WithShutdownGrace 设置优雅退出宽限。
func WithShutdownGrace(d time.Duration) Option {
	return func(c *Controller) {
		if d > 0 {
			c.ShutdownGrace = d
		}
	}
}

// NewController 创建 Controller。RoomID 为 gameroom 中分配的房间 id。
func NewController(sdk Lifecycle, mgr *gameroom.Manager, roomID string, opts ...Option) (*Controller, error) {
	if sdk == nil || mgr == nil {
		return nil, fmt.Errorf("agones: nil sdk or manager")
	}
	if roomID == "" {
		return nil, fmt.Errorf("agones: empty room id")
	}
	c := &Controller{SDK: sdk, Manager: mgr, RoomID: roomID, ShutdownGrace: 30 * time.Second}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Run 标记 GameServer Ready,并在 ctx 取消或 SDK Shutdown 时 Drain 房间。
// 典型用法:Agones 侧 goroutine 监听 shutdown 后 cancel ctx。
func (c *Controller) Run(ctx context.Context) error {
	if err := c.SDK.Ready(); err != nil {
		return fmt.Errorf("agones: ready: %w", err)
	}
	slog.Info("agones: gameserver ready", "room", c.RoomID)

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return c.shutdown(ctx)
		case <-ticker.C:
			if err := c.SDK.Health(); err != nil {
				slog.Warn("agones: health", "err", err)
			}
		}
	}
}

func (c *Controller) shutdown(ctx context.Context) error {
	slog.Info("agones: draining room", "room", c.RoomID)
	if err := c.Manager.Drain(c.RoomID); err != nil {
		slog.Warn("agones: drain", "err", err)
	}

	deadline, cancel := context.WithTimeout(ctx, c.ShutdownGrace)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c.Manager.Get(c.RoomID) == nil {
			break
		}
		select {
		case <-deadline.Done():
			slog.Warn("agones: shutdown grace exceeded", "room", c.RoomID)
			_ = c.Manager.Close(c.RoomID)
			goto done
		case <-ticker.C:
		}
	}
done:
	if err := c.SDK.Shutdown(); err != nil {
		return fmt.Errorf("agones: shutdown: %w", err)
	}
	slog.Info("agones: gameserver shutdown", "room", c.RoomID)
	return nil
}

// AllocateRoom 在 Manager 中分配房间并返回 Controller(尚未 Ready)。
func AllocateRoom(mgr *gameroom.Manager, spec gameroom.Spec) (*Handle, error) {
	h, err := mgr.Allocate(spec)
	if err != nil {
		return nil, err
	}
	return &Handle{Manager: mgr, Room: h}, nil
}

// Handle 绑定 Manager 与已分配房间。
type Handle struct {
	Manager *gameroom.Manager
	Room    *gameroom.Handle
}

// Attach 为已分配房间创建 Agones Controller。
func (h *Handle) Attach(sdk Lifecycle, opts ...Option) (*Controller, error) {
	if h == nil || h.Room == nil {
		return nil, fmt.Errorf("agones: nil handle")
	}
	return NewController(sdk, h.Manager, h.Room.ID, opts...)
}
