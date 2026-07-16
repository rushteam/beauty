// statesync demo:基于同一个 pkg/gameloop.Room,换个 Handler 就从帧同步切到「状态同步」。
//
// 与 examples/gameloop(帧同步:下发"输入")的唯一区别在 OnTick 的策略:
//   - 服务器权威模拟:每帧把玩家输入应用到世界坐标(World.Step),这里算出"结果状态";
//   - AOI(兴趣区域):每帧建一次 pkg/spatial 网格索引;每个连接在"出口"按自己玩家
//     的视野半径查询 Nearby,只把可视范围内的实体发给该客户端——下发的是"状态"不是"输入"。
//
// 关键设计:Room 每帧只广播一份"权威世界"(*worldTick,内部对象,不直接上线);
// 每个连接的写循环把它投影成各自的 View(AOI 过滤后、可序列化)再下发。即
// "内部全量扇出 + 出口按连接过滤"——状态同步 + AOI 的常见做法。
//
// 自带 3 个进程内 bot 验证 AOI:alice/bob 相邻(互相可见),carol 远处(谁也看不见它、它也看不见谁)。
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
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/spatial"
	"github.com/rushteam/beauty/pkg/ws"
)

const (
	addr       = "127.0.0.1:8124"
	tickRate   = 50 * time.Millisecond // 20Hz
	aoiRadius  = 100                   // 视野半径
	cellSize   = 100                   // spatial 网格单元(≈视野半径量级)
	worldBound = 1000
)

// Cmd 客户端上行:本帧移动增量。
type Cmd struct {
	DX float64 `json:"dx"`
	DY float64 `json:"dy"`
}

// Entity 一个实体的坐标(下发给客户端,可序列化)。
type Entity struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

// worldTick 是服务器每帧的权威结果——内部扇出用,持有当帧的只读空间索引,不直接上线。
type worldTick struct {
	frame uint64
	index *spatial.Index[string] // 建好后不再变更,可被多个连接并发只读查询
}

// View 是每个客户端实际收到的(AOI 过滤后)。
type View struct {
	Frame   uint64   `json:"frame"`
	Self    Entity   `json:"self"`
	Visible []Entity `json:"visible"`
}

// World 权威世界:只在 OnTick(tick goroutine)里推进坐标,Join/Leave 来自连接 goroutine,故加锁。
type World struct {
	mu  sync.Mutex
	pos map[string]Entity
}

func newWorld() *World { return &World{pos: make(map[string]Entity)} }

func (w *World) Join(id string, x, y float64) {
	w.mu.Lock()
	w.pos[id] = Entity{ID: id, X: x, Y: y}
	w.mu.Unlock()
}

func (w *World) Leave(id string) {
	w.mu.Lock()
	delete(w.pos, id)
	w.mu.Unlock()
}

// Step 应用本帧输入(权威模拟),再快照成一个只读空间索引。
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
	}
	ix := spatial.New[string](cellSize)
	for id, p := range w.pos {
		ix.Add(id, p.X, p.Y)
	}
	return &worldTick{frame: frame, index: ix}
}

// spawns 决定每个玩家的出生点(演示用,让 AOI 结果可预期)。
var spawns = map[string]Entity{
	"alice": {X: 50, Y: 50},
	"bob":   {X: 120, Y: 120}, // 距 alice ≈99 < 100 → 互相可见
	"carol": {X: 800, Y: 800}, // 远处 → 谁也看不见
}

func main() {
	world := newWorld()

	// 状态同步策略:每帧跑权威模拟,广播一份只读世界快照(供各连接做 AOI 投影)。
	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[Cmd, *worldTick](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []*worldTick {
			return []*worldTick{world.Step(frame, inputs)}
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

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		ch, unsub := room.Subscribe(ctx)
		defer unsub()

		// 写循环:把每帧权威世界投影成本连接玩家的 AOI 视图后下发。
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
						continue // 本帧还没纳入(刚 Join 的竞态)
					}
					near := tk.index.Nearby(px, py, aoiRadius, player) // ← AOI 过滤
					view := View{Frame: tk.frame, Self: Entity{player, px, py}, Visible: toEntities(near)}
					if err := c.WriteJSON(ctx, view); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		// 读循环:把移动输入投进房间,等下一 tick 由权威模拟应用。
		for {
			var cmd Cmd
			if err := c.ReadJSON(ctx, &cmd); err != nil {
				return err
			}
			room.Push(player, cmd)
		}
	}))

	app := beauty.New(
		beauty.WithService(room),
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("statesync-demo")),
	)

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	<-room.Ready()

	players := []string{"alice", "bob", "carol"}
	botCtx, botCancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer botCancel()

	var wg sync.WaitGroup
	visible := make([]([]string), len(players))
	for i, p := range players {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			v, err := runBot(botCtx, "ws://"+addr+"/ws", p)
			if err != nil {
				log.Printf("bot %s: %v", p, err)
				return
			}
			visible[i] = v
		}(i, p)
	}
	wg.Wait()

	verifyAOI(players, visible)

	cancel()
	<-appErr
}

// runBot 连上 ws,持续收 View(不移动),返回最后一帧看到的可视实体 ID(排序)。
func runBot(ctx context.Context, url, player string) ([]string, error) {
	c, err := dialRetry(ctx, url+"?player="+player)
	if err != nil {
		return nil, err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	var (
		mu       sync.Mutex
		lastSeen []string
		lastAt   uint64
	)
	go func() {
		for {
			var v View
			if err := wsjson.Read(ctx, c, &v); err != nil {
				return
			}
			ids := make([]string, 0, len(v.Visible))
			for _, e := range v.Visible {
				ids = append(ids, e.ID)
			}
			slices.Sort(ids)
			mu.Lock()
			if v.Frame >= lastAt {
				lastAt, lastSeen = v.Frame, ids
			}
			mu.Unlock()
		}
	}()

	<-ctx.Done()
	mu.Lock()
	defer mu.Unlock()
	return lastSeen, nil
}

func verifyAOI(players []string, visible [][]string) {
	expected := map[string][]string{
		"alice": {"bob"},
		"bob":   {"alice"},
		"carol": {},
	}
	fmt.Println("──────── AOI(状态同步)校验 ────────")
	allOK := true
	for i, p := range players {
		got := visible[i]
		want := expected[p]
		ok := slices.Equal(got, want)
		allOK = allOK && ok
		mark := "✅"
		if !ok {
			mark = "❌"
		}
		fmt.Printf("  %-6s 视野内: %-16v (期望 %v) %s\n", p, fmtIDs(got), fmtIDs(want), mark)
	}
	if allOK {
		fmt.Println("结论: ✅ 每个客户端只收到视野内实体(AOI 过滤生效)")
	} else {
		fmt.Println("结论: ❌ AOI 结果与预期不符")
	}
	fmt.Println("────────────────────────────────────")
}

func toEntities(es []spatial.Entity[string]) []Entity {
	out := make([]Entity, 0, len(es))
	for _, e := range es {
		out = append(out, Entity{ID: e.ID, X: e.X, Y: e.Y})
	}
	return out
}

func fmtIDs(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	return fmt.Sprint(ids)
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
