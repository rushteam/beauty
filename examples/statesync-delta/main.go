// statesync-delta demo:在 statesync 基础上用 pkg/replicate 做 AOI 增量下发,
// 并用 pkg/inputclock + pkg/snapbuf + pkg/lagcomp 演示延迟补偿查询。
//
// 运行:go run ./examples/statesync-delta
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/gameloop"
	"github.com/rushteam/beauty/pkg/inputclock"
	"github.com/rushteam/beauty/pkg/lagcomp"
	"github.com/rushteam/beauty/pkg/replicate"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/snapbuf"
	"github.com/rushteam/beauty/pkg/spatial"
	"github.com/rushteam/beauty/pkg/ws"
)

const (
	addr       = "127.0.0.1:8125"
	tickRate   = 50 * time.Millisecond
	aoiRadius  = 100
	cellSize   = 100
	worldBound = 1000
)

type Cmd struct {
	DX          float64 `json:"dx"`
	DY          float64 `json:"dy"`
	ClientFrame uint64  `json:"client_frame,omitempty"`
}

type Entity struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

type worldTick struct {
	frame   uint64
	index   *spatial.Index[string]
	dirty   []string
	removed []string
	lookup  replicate.Lookup[string]
}

type World struct {
	mu       sync.Mutex
	pos      map[string]Entity
	dirty    *replicate.DirtySet[string]
	versions *replicate.Versions[string]
	snaps    *snapbuf.Ring[map[string]Entity]
}

func newWorld() *World {
	return &World{
		pos:      make(map[string]Entity),
		dirty:    replicate.NewDirtySet[string](),
		versions: replicate.NewVersions[string](),
		snaps:    snapbuf.New[map[string]Entity](64),
	}
}

func (w *World) Join(id string, x, y float64) {
	w.mu.Lock()
	w.pos[id] = Entity{ID: id, X: x, Y: y}
	w.dirty.Mark(id)
	w.versions.Bump(id)
	w.mu.Unlock()
}

func (w *World) Leave(id string) {
	w.mu.Lock()
	delete(w.pos, id)
	w.dirty.Remove(id)
	w.versions.Delete(id)
	w.mu.Unlock()
}

func (w *World) Step(frame uint64, inputs []gameloop.PlayerInput[Cmd], clock *inputclock.Clock) *worldTick {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, in := range inputs {
		if in.ClientFrame > 0 {
			clock.Record(inputclock.Sample{
				Player: in.Player, ClientFrame: in.ClientFrame,
				ServerFrame: frame, ReceivedAt: in.ReceivedAt,
			})
		}
		p, ok := w.pos[in.Player]
		if !ok {
			continue
		}
		p.X = clamp(p.X+in.Input.DX, 0, worldBound)
		p.Y = clamp(p.Y+in.Input.DY, 0, worldBound)
		w.pos[in.Player] = p
		w.dirty.Mark(in.Player)
		w.versions.Bump(in.Player)
	}
	snapCopy := make(map[string]Entity, len(w.pos))
	for id, p := range w.pos {
		snapCopy[id] = p
	}
	w.snaps.Push(frame, snapCopy)

	ix := spatial.New[string](cellSize)
	for id, p := range w.pos {
		ix.Add(id, p.X, p.Y)
	}
	dirty, removed := w.dirty.Consume()
	lookup := func(id string) (replicate.EntityState, bool) {
		p, ok := w.pos[id]
		if !ok {
			return replicate.EntityState{}, false
		}
		return replicate.EntityState{
			ID: id, X: p.X, Y: p.Y, Version: w.versions.Get(id),
		}, true
	}
	return &worldTick{frame: frame, index: ix, dirty: dirty, removed: removed, lookup: lookup}
}

var spawns = map[string]Entity{
	"alice": {X: 50, Y: 50},
	"bob":   {X: 120, Y: 120},
	"carol": {X: 800, Y: 800},
}

func main() {
	world := newWorld()
	clock := inputclock.New()
	projector := replicate.NewProjector[string](replicate.Config{})
	comp := &lagcomp.Compensator[map[string]Entity]{Clock: clock, Ring: world.snaps, Tick: tickRate}

	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[Cmd, *worldTick](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []*worldTick {
			return []*worldTick{world.Step(frame, inputs, clock)}
		}),
		gameloop.WithName("statesync-delta"),
	)

	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
		player := r.URL.Query().Get("player")
		sp, ok := spawns[player]
		if !ok {
			sp = Entity{X: worldBound / 2, Y: worldBound / 2}
		}
		world.Join(player, sp.X, sp.Y)
		defer world.Leave(player)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		ch, unsub := room.Subscribe(ctx)
		defer unsub()

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case tk, ok := <-ch:
					if !ok {
						return
					}
					px, py, ok := tk.index.Pos(player)
					if !ok {
						continue
					}
					visible := tk.index.Nearby(px, py, aoiRadius, player)
					delta := projector.Project(tk.frame, player, visible, tk.dirty, tk.removed, tk.lookup)
					if err := c.WriteJSON(ctx, delta); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		for {
			var cmd Cmd
			if err := c.ReadJSON(ctx, &cmd); err != nil {
				return err
			}
			room.PushInput(gameloop.PlayerInput[Cmd]{
				Player: player, Input: cmd, ClientFrame: cmd.ClientFrame,
			})
			if cmd.ClientFrame > 0 {
				if _, _, ok := comp.WorldAt(player, cmd.ClientFrame); ok {
					// 演示:延迟补偿查询可用(命中判定等业务在此使用)
				}
			}
		}
	}))

	app := beauty.New(
		beauty.WithService(room),
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("statesync-delta")),
	)

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	<-room.Ready()

	players := []string{"alice", "bob", "carol"}
	botCtx, botCancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer botCancel()

	var wg sync.WaitGroup
	baseline := make([]bool, len(players))
	for i, p := range players {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			b, err := runBot(botCtx, "ws://"+addr+"/ws", p)
			if err != nil {
				log.Printf("bot %s: %v", p, err)
				return
			}
			baseline[i] = b
		}(i, p)
	}
	wg.Wait()

	fmt.Println("──────── 增量同步校验 ────────")
	for i, p := range players {
		mark := "✅"
		if !baseline[i] {
			mark = "❌"
		}
		fmt.Printf("  %-6s 首包 baseline=%v %s\n", p, baseline[i], mark)
	}

	cancel()
	<-appErr
}

func runBot(ctx context.Context, url, player string) (bool, error) {
	c, err := dialRetry(ctx, url+"?player="+player)
	if err != nil {
		return false, err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	var gotBaseline bool
	for {
		var d replicate.Delta
		if err := wsjson.Read(ctx, c, &d); err != nil {
			return gotBaseline, err
		}
		if d.Baseline {
			gotBaseline = true
			break
		}
	}
	return gotBaseline, nil
}

func dialRetry(ctx context.Context, url string) (*websocket.Conn, error) {
	var lastErr error
	for range 20 {
		c, _, err := websocket.Dial(ctx, url, nil)
		if err == nil {
			return c, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, lastErr
}

func clamp(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}
