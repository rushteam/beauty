// agones-room demo:gameroom + gameloop + contrib/agones 本地可跑通。
//
// 默认 dev 模式(无需 K8s sidecar):模拟 Agones Ready/Shutdown,Ctrl+C 触发 Drain。
// 设 BEAUTY_USE_AGONES_SDK=1 且 sidecar 可达时使用真实 SDK。
//
// 运行:
//
//	go run ./examples/agones-room
//
// 连接: ws://127.0.0.1:8130/ws?player=alice
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/agones"
	"github.com/rushteam/beauty/pkg/game/gameloop"
	"github.com/rushteam/beauty/pkg/game/gameroom"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/transport/ws"
)

const addr = "127.0.0.1:8130"

type Cmd struct {
	DX float64 `json:"dx"`
	DY float64 `json:"dy"`
}

type Frame struct {
	Frame  uint64                      `json:"frame"`
	Inputs []gameloop.PlayerInput[Cmd] `json:"inputs"`
}

func main() {
	roomID := os.Getenv("HOSTNAME")
	if roomID == "" {
		roomID = "local-dev"
	}

	loop := gameloop.New(50*time.Millisecond,
		gameloop.HandlerFunc[Cmd, Frame](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []Frame {
			return []Frame{{Frame: frame, Inputs: inputs}}
		}),
		gameloop.WithName(roomID),
	)

	mgr := gameroom.New(gameroom.WithHooks(gameroom.Hooks{
		OnRunning: func(ctx context.Context, id string) error {
			slog.Info("room running, accepting players", "room", id)
			return nil
		},
		OnDrain: func(id string) {
			slog.Info("room draining", "room", id)
		},
		OnClose: func(id string) {
			slog.Info("room closed", "room", id)
		},
	}))
	defer mgr.Stop()

	handle, err := agones.AllocateRoom(mgr, gameroom.Spec{ID: roomID, MaxPlayers: 8})
	if err != nil {
		log.Fatal(err)
	}

	sdk, err := openSDK()
	if err != nil {
		log.Fatal(err)
	}

	ctrl, err := handle.Attach(sdk, agones.WithShutdownGrace(10*time.Second))
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := ctrl.Run(ctx); err != nil {
			slog.Error("agones controller", "err", err)
		}
		stop()
	}()

	// 房间 Ready 后自动开局(演示 ScheduleStart;生产可改为全员 Ready)。
	_ = mgr.ScheduleStart(roomID, 500*time.Millisecond)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		h := mgr.Get(roomID)
		if h == nil {
			http.Error(w, "closed", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, "phase=%s\n", h.Phase)
	})
	mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
		player := r.URL.Query().Get("player")
		if player == "" {
			return fmt.Errorf("player query required")
		}
		h := mgr.Get(roomID)
		if h == nil || h.Phase == gameroom.PhaseDraining || h.Phase == gameroom.PhaseClosed {
			return fmt.Errorf("room not accepting players")
		}
		if err := mgr.Join(roomID, player); err != nil {
			return err
		}
		defer mgr.Leave(roomID, player)

		runCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		ch, unsub := loop.Subscribe(runCtx)
		defer unsub()

		go func() {
			for {
				select {
				case <-runCtx.Done():
					return
				case fr, ok := <-ch:
					if !ok {
						return
					}
					if err := c.WriteJSON(runCtx, fr); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		for {
			var cmd Cmd
			if err := c.ReadJSON(runCtx, &cmd); err != nil {
				return err
			}
			loop.Push(player, cmd)
		}
	}))

	app := beauty.New(
		beauty.WithService(loop),
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("agones-room")),
	)

	slog.Info("agones-room listening", "addr", addr, "room", roomID)
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
}

func openSDK() (agones.Lifecycle, error) {
	if os.Getenv("BEAUTY_USE_AGONES_SDK") == "1" {
		return agones.NewSDK()
	}
	return &devSDK{}, nil
}

// devSDK 本地开发用 Lifecycle+Watcher(Ctrl+C 或 SIGTERM → Shutdown)。
type devSDK struct{}

func (d *devSDK) Ready() error {
	slog.Info("agones dev: ready (no sidecar)")
	return nil
}

func (d *devSDK) Shutdown() error {
	slog.Info("agones dev: shutdown")
	return nil
}

func (d *devSDK) Health() error { return nil }
