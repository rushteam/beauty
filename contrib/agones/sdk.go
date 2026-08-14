package agones

import (
	"context"

	gamesdk "agones.dev/agones/pkg/sdk"
	agonesdk "agones.dev/agones/sdks/go"
)

// SDK 是真实 Agones Go SDK 的 Lifecycle 适配。
type SDK struct {
	inner *agonesdk.SDK
}

// NewSDK 连接 sidecar 并创建 SDK。
func NewSDK() (*SDK, error) {
	s, err := agonesdk.NewSDK()
	if err != nil {
		return nil, err
	}
	return &SDK{inner: s}, nil
}

func (s *SDK) Ready() error    { return s.inner.Ready() }
func (s *SDK) Shutdown() error { return s.inner.Shutdown() }
func (s *SDK) Health() error   { return s.inner.Health() }

// WatchContext 在 GameServer 进入 Shutdown 时 cancel ctx。
func (s *SDK) WatchContext(ctx context.Context) (context.Context, context.CancelFunc) {
	child, cancel := context.WithCancel(ctx)
	go func() {
		_ = s.inner.WatchGameServer(func(gs *gamesdk.GameServer) {
			if gs != nil && gs.Status != nil && gs.Status.State == "Shutdown" {
				cancel()
			}
		})
	}()
	return child, cancel
}

var _ Lifecycle = (*SDK)(nil)
