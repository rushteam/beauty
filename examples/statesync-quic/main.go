// statesync-quic demo:把状态同步搬到 QUIC,用一条连接的两种通道分流两类数据。
//
//   - 关键指令走【可靠有序流】:客户端开一条流,先发 Hello{player} 握手,之后持续发
//     Cmd(移动指令)。丢不得、要顺序,用可靠流(像 TCP 但无跨流队头阻塞)。
//   - 高频状态走【不可靠数据报】:服务器每帧把该玩家 AOI 视野内的实体打成 View 用
//     SendDatagram 下发。丢了就丢、不重传、不阻塞后续——正是位置/状态更新想要的。
//
// 逻辑与 examples/statesync(WebSocket 版)一致:pkg/gameloop 定步长权威模拟 +
// pkg/spatial 每帧 AOI 过滤;区别只在传输层换成 pkg/quic 并按可靠性分了两条通道。
//
// 自带 3 个进程内 QUIC bot 端到端验证 AOI:alice/bob 相邻互见、carol 远处隔离。
//
// 运行:go run ./examples/statesync-quic
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/gameloop"
	"github.com/rushteam/beauty/pkg/quic"
	"github.com/rushteam/beauty/pkg/spatial"
)

const (
	addr       = "127.0.0.1:8443"
	tickRate   = 50 * time.Millisecond
	aoiRadius  = 100
	cellSize   = 100
	worldBound = 1000
)

// Hello 是可靠流上的第一条消息:自报玩家身份(QUIC 没有 URL query,用握手identify)。
type Hello struct {
	Player string `json:"player"`
}

// Cmd 客户端上行的移动指令(可靠流上 Hello 之后的消息流)。
type Cmd struct {
	DX float64 `json:"dx"`
	DY float64 `json:"dy"`
}

// Entity / View / worldTick / World 同 WebSocket 版(权威模拟 + AOI)。
type Entity struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

type View struct {
	Frame   uint64   `json:"frame"`
	Self    Entity   `json:"self"`
	Visible []Entity `json:"visible"`
}

type worldTick struct {
	frame uint64
	index *spatial.Index[string]
}

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

var spawns = map[string]Entity{
	"alice": {X: 50, Y: 50},
	"bob":   {X: 120, Y: 120}, // 距 alice ≈99 < 100 → 互相可见
	"carol": {X: 800, Y: 800}, // 远处 → 谁也看不见
}

func main() {
	world := newWorld()

	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[Cmd, *worldTick](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []*worldTick {
			return []*worldTick{world.Step(frame, inputs)}
		}),
		gameloop.WithName("statesync-quic"),
	)

	srv := quic.NewServer(addr, func(ctx context.Context, c *quic.Conn) error {
		// 1) 握手:等客户端开控制流并发 Hello,识别玩家。
		stream, err := c.AcceptStream(ctx)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(stream)
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

		// 2) 状态下行(不可靠数据报):每帧把 AOI 视野内实体发给该玩家。
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
					near := tk.index.Nearby(px, py, aoiRadius, player)
					b, err := json.Marshal(View{Frame: tk.frame, Self: Entity{player, px, py}, Visible: toEntities(near)})
					if err != nil || len(b) > 1000 {
						continue // 数据报受 MTU 限制;过大应改走可靠流或分片(此处从简跳过)
					}
					_ = c.SendDatagram(b) // 不可靠:丢了等下一帧,不重传不阻塞
				}
			}
		}()

		// 3) 指令上行(可靠有序流):Hello 之后持续读 Cmd 投进房间。
		for {
			var cmd Cmd
			if err := dec.Decode(&cmd); err != nil {
				return err
			}
			room.Push(player, cmd)
		}
	}, quic.WithServiceName("statesync-quic"))

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
	visible := make([][]string, len(players))
	for i, p := range players {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			v, err := runBot(botCtx, addr, p)
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

// runBot:QUIC 连接 → 开可靠流发 Hello(+周期 Cmd)→ 收状态数据报,返回最后一帧可视实体。
func runBot(ctx context.Context, addr, player string) ([]string, error) {
	c, err := dialRetry(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer c.Close("bye")

	stream, err := c.OpenStream(ctx)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(stream)
	if err := enc.Encode(Hello{Player: player}); err != nil { // 握手
		return nil, err
	}

	// 周期发零位移 Cmd(演示可靠指令通道;位置不变以便 AOI 结果可预期)。
	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = enc.Encode(Cmd{})
			}
		}
	}()

	// 收状态数据报,记录最新一帧的可视集合。
	var (
		mu       sync.Mutex
		lastSeen []string
		lastAt   uint64
	)
	go func() {
		for {
			b, err := c.ReceiveDatagram(ctx)
			if err != nil {
				return
			}
			var v View
			if json.Unmarshal(b, &v) != nil {
				continue
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
	expected := map[string][]string{"alice": {"bob"}, "bob": {"alice"}, "carol": {}}
	fmt.Println("──────── AOI(状态同步 / QUIC)校验 ────────")
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
		fmt.Println("结论: ✅ 指令走可靠流、状态走数据报,AOI 端到端一致")
	} else {
		fmt.Println("结论: ❌ 与预期不符")
	}
	fmt.Println("──────────────────────────────────────────")
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
