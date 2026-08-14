// statesync-quic-delta:QUIC 双通道 + replicate 增量 + CatchUp/Ack。
//
//   - 可靠流:Hello / Cmd / Ack / CatchUp
//   - 不可靠数据报:Delta(丢了靠 Ack+CatchUp 补)
//
// 运行:go run ./examples/statesync-quic-delta
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/gameloop"
	"github.com/rushteam/beauty/pkg/quic"
	"github.com/rushteam/beauty/pkg/replicate"
	"github.com/rushteam/beauty/pkg/spatial"
)

const (
	addr       = "127.0.0.1:8444"
	tickRate   = 50 * time.Millisecond
	aoiRadius  = 100
	cellSize   = 100
	worldBound = 1000
)

type Hello struct {
	Player string `json:"player"`
}

type Cmd struct {
	DX float64 `json:"dx"`
	DY float64 `json:"dy"`
}

type ClientMsg struct {
	Kind string         `json:"kind"`
	Cmd  *Cmd           `json:"cmd,omitempty"`
	Ack  *replicate.Ack `json:"ack,omitempty"`
}

type ServerMsg struct {
	Kind    string                  `json:"kind"`
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
}

func newWorld() *World {
	return &World{
		pos:      make(map[string]Entity),
		dirty:    replicate.NewDirtySet[string](),
		versions: replicate.NewVersions[string](),
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

func (w *World) Step(frame uint64, inputs []gameloop.PlayerInput[Cmd]) *worldTick {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, in := range inputs {
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
		return replicate.EntityState{ID: id, X: p.X, Y: p.Y, Version: w.versions.Get(id)}, true
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
	projector := replicate.NewProjector[string](replicate.Config{})
	tracks := sync.Map{}

	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[Cmd, *worldTick](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []*worldTick {
			return []*worldTick{world.Step(frame, inputs)}
		}),
		gameloop.WithName("statesync-quic-delta"),
	)

	srv := quic.NewServer(addr, func(ctx context.Context, c *quic.Conn) error {
		stream, err := c.AcceptStream(ctx)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(stream)
		enc := json.NewEncoder(stream)

		var hello Hello
		if err := dec.Decode(&hello); err != nil {
			return err
		}
		player := hello.Player
		sp, ok := spawns[player]
		if !ok {
			sp = Entity{X: worldBound / 2, Y: worldBound / 2}
		}
		world.Join(player, sp.X, sp.Y)
		defer world.Leave(player)
		tr, _ := tracks.LoadOrStore(player, replicate.NewViewerTrack(replicate.NewJournal(64)))
		track := tr.(*replicate.ViewerTrack)
		defer tracks.Delete(player)

		go func() {
			ch, unsub := room.Subscribe(ctx)
			defer unsub()
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
					track.RecordSent(delta)
					b, err := json.Marshal(delta)
					if err != nil || len(b) > 900 {
						continue
					}
					_ = c.SendDatagram(b)
				}
			}
		}()

		for {
			var msg ClientMsg
			if err := dec.Decode(&msg); err != nil {
				return err
			}
			switch msg.Kind {
			case "ack":
				if msg.Ack == nil {
					continue
				}
				batch := track.OnAck(*msg.Ack)
				if len(batch.Deltas) > 0 {
					_ = enc.Encode(ServerMsg{Kind: "catchup", CatchUp: &batch})
				}
			case "", "cmd":
				cmd := Cmd{}
				if msg.Cmd != nil {
					cmd = *msg.Cmd
				}
				room.Push(player, cmd)
			}
		}
	}, quic.WithServiceName("statesync-quic-delta"))

	app := beauty.New(beauty.WithService(room), beauty.WithService(srv))

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	<-room.Ready()
	<-srv.Ready()

	players := []string{"alice", "bob", "carol"}
	botCtx, botCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer botCancel()

	var wg sync.WaitGroup
	baseline := make([]bool, len(players))
	for i, p := range players {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			baseline[i] = runBot(botCtx, addr, p)
		}(i, p)
	}
	wg.Wait()

	fmt.Println("──────── QUIC 增量 + CatchUp ────────")
	allOK := true
	for i, p := range players {
		ok := baseline[i]
		allOK = allOK && ok
		mark := "✅"
		if !ok {
			mark = "❌"
		}
		fmt.Printf("  %-6s baseline=%v %s\n", p, ok, mark)
	}
	if allOK {
		fmt.Println("结论: ✅ Delta 走数据报、Ack/CatchUp 走可靠流")
	}
	cancel()
	<-appErr
}

func runBot(ctx context.Context, addr, player string) bool {
	c, err := dialRetry(ctx, addr)
	if err != nil {
		log.Printf("bot %s: %v", player, err)
		return false
	}
	defer c.Close("bye")

	stream, err := c.OpenStream(ctx)
	if err != nil {
		return false
	}
	dec := json.NewDecoder(stream)
	enc := json.NewEncoder(stream)
	_ = enc.Encode(Hello{Player: player})

	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = enc.Encode(ClientMsg{Kind: "cmd", Cmd: &Cmd{}})
			}
		}
	}()

	var gotBaseline bool
	var lastFrame uint64
	go func() {
		for {
			b, err := c.ReceiveDatagram(ctx)
			if err != nil {
				return
			}
			var d replicate.Delta
			if json.Unmarshal(b, &d) != nil {
				continue
			}
			if d.Baseline {
				gotBaseline = true
			}
			if d.Frame > lastFrame {
				lastFrame = d.Frame
			}
		}
	}()

	go func() {
		for {
			var msg ServerMsg
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.Kind == "catchup" && msg.CatchUp != nil {
				for _, d := range msg.CatchUp.Deltas {
					if d.Frame > lastFrame {
						lastFrame = d.Frame
					}
				}
			}
		}
	}()

	<-ctx.Done()
	if gotBaseline && lastFrame > 0 {
		_ = enc.Encode(ClientMsg{Kind: "ack", Ack: &replicate.Ack{LastFrame: lastFrame}})
	}
	return gotBaseline
}

func dialRetry(ctx context.Context, addr string) (*quic.Conn, error) {
	var lastErr error
	for range 40 {
		c, err := quic.Dial(ctx, addr, quic.WithInsecureSkipVerify(true))
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
