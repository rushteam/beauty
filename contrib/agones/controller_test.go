package agones_test

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/agones"
	"github.com/rushteam/beauty/pkg/gameroom"
)

type mockSDK struct {
	ready    int
	shutdown int
}

func (m *mockSDK) Ready() error    { m.ready++; return nil }
func (m *mockSDK) Shutdown() error { m.shutdown++; return nil }
func (m *mockSDK) Health() error   { return nil }

func TestControllerShutdown(t *testing.T) {
	mgr := gameroom.New()
	defer mgr.Stop()
	h, err := agones.AllocateRoom(mgr, gameroom.Spec{ID: "gs-1", MaxPlayers: 4})
	if err != nil {
		t.Fatal(err)
	}
	sdk := &mockSDK{}
	ctrl, err := h.Attach(sdk, agones.WithShutdownGrace(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ctrl.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sdk.ready != 1 || sdk.shutdown != 1 {
		t.Fatalf("ready=%d shutdown=%d", sdk.ready, sdk.shutdown)
	}
}
