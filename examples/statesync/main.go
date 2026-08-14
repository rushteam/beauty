// statesync demo:权威模拟 + spatial AOI + pkg/replicate 增量同步 + CatchUp/Ack。
//
// 协议:ClientMsg{kind:cmd|ack|resync} / ServerMsg{kind:delta|catchup}
// catchup.truncated=true 时客户端发 resync,服务器 DropViewer 下帧重发 baseline。
//
// 运行:go run ./examples/statesync
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
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
	addr       = "127.0.0.1:8124"
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

type ClientMsg struct {
	Kind string         `json:"kind"` // cmd | ack | resync
	Cmd  *Cmd           `json:"cmd,omitempty"`
	Ack  *replicate.Ack `json:"ack,omitempty"`
}

type ServerMsg struct {
	Kind    string                  `json:"kind"` // delta | catchup
	Delta   *replicate.Delta        `json:"delta,omitempty"`
	CatchUp *replicate.CatchUpBatch `json:"catchup,omitempty"`
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

type botResult struct {
	baseline, catchup bool
	visible           []string
}

func main() {
	world := newWorld()
	clock := inputclock.New()
	projector := replicate.NewProjector[string](replicate.Config{})
	tracks := sync.Map{}
	comp := &lagcomp.Compensator[map[string]Entity]{Clock: clock, Ring: world.snaps, Tick: tickRate}

	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[Cmd, *worldTick](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []*worldTick {
			return []*worldTick{world.Step(frame, inputs, clock)}
		}),
		gameloop.WithName("statesync"),
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

		storeTrack := func() *replicate.ViewerTrack {
			tr, _ := tracks.LoadOrStore(player, replicate.NewViewerTrack(replicate.NewJournal(64)))
			return tr.(*replicate.ViewerTrack)
		}
		storeTrack()
		defer tracks.Delete(player)

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
					tr, _ := tracks.Load(player)
					if tr == nil {
						continue
					}
					track := tr.(*replicate.ViewerTrack)
					track.RecordSent(delta)
					if err := c.WriteJSON(ctx, ServerMsg{Kind: "delta", Delta: &delta}); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		for {
			var msg ClientMsg
			if err := c.ReadJSON(ctx, &msg); err != nil {
				return err
			}
			switch msg.Kind {
			case "ack":
				if msg.Ack == nil {
					continue
				}
				tr, _ := tracks.Load(player)
				if tr == nil {
					continue
				}
				track := tr.(*replicate.ViewerTrack)
				batch := track.OnAck(*msg.Ack)
				if len(batch.Deltas) > 0 || batch.Truncated {
					if err := c.WriteJSON(ctx, ServerMsg{Kind: "catchup", CatchUp: &batch}); err != nil {
						return err
					}
				}
			case "resync":
				projector.DropViewer(player)
				tracks.Store(player, replicate.NewViewerTrack(replicate.NewJournal(64)))
			case "", "cmd":
				cmd := Cmd{}
				if msg.Cmd != nil {
					cmd = *msg.Cmd
				}
				room.PushInput(gameloop.PlayerInput[Cmd]{
					Player: player, Input: cmd, ClientFrame: cmd.ClientFrame,
				})
				if cmd.ClientFrame > 0 {
					_, _, _ = comp.WorldAt(player, cmd.ClientFrame)
				}
			default:
				continue
			}
		}
	}))

	app := beauty.New(
		beauty.WithService(room),
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("statesync")),
	)

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	<-room.Ready()

	players := []string{"alice", "bob", "carol"}
	botCtx, botCancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer botCancel()

	results := make([]botResult, len(players))
	var wg sync.WaitGroup
	for i, p := range players {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			r, err := runBot(botCtx, "ws://"+addr+"/ws", p)
			if err != nil {
				log.Printf("bot %s: %v", p, err)
				return
			}
			results[i] = r
		}(i, p)
	}
	wg.Wait()

	fmt.Println("──────── 状态同步(replicate + CatchUp) ────────")
	allOK := true
	expected := map[string][]string{"alice": {"bob"}, "bob": {"alice"}, "carol": {}}
	for i, p := range players {
		r := results[i]
		aoiOK := slices.Equal(r.visible, expected[p])
		ok := r.baseline && r.catchup && aoiOK
		allOK = allOK && ok
		mark := "✅"
		if !ok {
			mark = "❌"
		}
		fmt.Printf("  %-6s baseline=%v catchup=%v AOI=%v %s\n", p, r.baseline, r.catchup, r.visible, mark)
	}
	if allOK {
		fmt.Println("结论: ✅ 增量 Delta + Ack/CatchUp + AOI 端到端一致")
	}

	cancel()
	<-appErr
}

func runBot(ctx context.Context, url, player string) (botResult, error) {
	c, err := dialRetry(ctx, url+"?player="+player)
	if err != nil {
		return botResult{}, err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	var out botResult
	var lastFrame uint64
	deadline := time.After(1500 * time.Millisecond)

waitLoop:
	for {
		select {
		case <-ctx.Done():
			break waitLoop
		case <-deadline:
			break waitLoop
		default:
		}
		readCtx, readCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		var msg ServerMsg
		err := wsjson.Read(readCtx, c, &msg)
		readCancel()
		if err != nil {
			if out.baseline && lastFrame >= 3 {
				break waitLoop
			}
			continue
		}
		if msg.Kind == "delta" && msg.Delta != nil {
			if msg.Delta.Baseline {
				out.baseline = true
				out.visible = spawnIDs(msg.Delta)
			}
			if msg.Delta.Frame > lastFrame {
				lastFrame = msg.Delta.Frame
			}
		}
		if out.baseline && lastFrame >= 3 {
			break waitLoop
		}
	}

	if out.baseline && lastFrame >= 3 {
		_ = wsjson.Write(ctx, c, ClientMsg{Kind: "ack", Ack: &replicate.Ack{LastFrame: 1}})
		catchCtx, catchCancel := context.WithTimeout(ctx, 800*time.Millisecond)
		defer catchCancel()
		for {
			var msg ServerMsg
			if err := wsjson.Read(catchCtx, c, &msg); err != nil {
				break
			}
			if msg.Kind != "catchup" || msg.CatchUp == nil {
				continue
			}
			out.catchup = len(msg.CatchUp.Deltas) > 0
			if msg.CatchUp.Truncated {
				_ = wsjson.Write(ctx, c, ClientMsg{Kind: "resync"})
			}
			break
		}
	}
	return out, nil
}

func spawnIDs(d *replicate.Delta) []string {
	ids := make([]string, 0, len(d.Spawn))
	for _, e := range d.Spawn {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	return ids
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

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
