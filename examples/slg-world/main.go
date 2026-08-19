// slg-world 示例:SLG 大地图综合 demo。
//
// 演示如何用 Beauty 原语组合出 SLG 服务端核心架构:
//
//	gameloop.Room        — 大地图主循环(tick 驱动行军移动、AOI 推送)
//	spatial.Index + aoi  — 九宫格式 AOI 视野管理(enter/leave/stay)
//	timerqueue.Queue     — 万级建筑/科技倒计时(最小堆单协程驱动)
//	scheduler.Scheduler  — 异步战斗计算(worker pool,不阻塞主循环)
//	pkg/ws               — WebSocket 网关(每连接读写分离)
//
// 自带 3 个 bot(alice/bob/carol)自动验证:
//
//	alice 发起行军攻打 bob → 部队沿途 AOI 可见 → 到达后异步战斗结算
//	alice 发起建筑升级 → timerqueue 倒计时完成
//	carol 距离远,不在 alice/bob 的 AOI 内
//
// 运行:go run ./examples/slg-world
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/game/gameloop"
	"github.com/rushteam/beauty/pkg/game/spatial"
	"github.com/rushteam/beauty/pkg/game/spatial/aoi"
	"github.com/rushteam/beauty/pkg/orchestration/scheduler"
	"github.com/rushteam/beauty/pkg/orchestration/timerqueue"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/transport/ws"
)

// ── 常量 ──────────────────────────────────────────────

const (
	addr       = "127.0.0.1:8190"
	tickRate   = 50 * time.Millisecond // 20 Hz
	aoiRadius  = 300.0
	cellSize   = 200.0
	troopSpeed = 200.0 // 单位/秒
	buildTime  = 400 * time.Millisecond
)

// ── 协议 ──────────────────────────────────────────────

type ClientMsg struct {
	Kind    string  `json:"kind"`               // march | build
	TargetX float64 `json:"target_x,omitempty"` // march 目标坐标
	TargetY float64 `json:"target_y,omitempty"`
}

type ServerMsg struct {
	Frame   uint64       `json:"frame"`
	Visible []EntityView `json:"visible"`          // AOI 内可见实体
	Events  []GameEvent  `json:"events,omitempty"` // 本帧事件
}

type EntityView struct {
	ID    string  `json:"id"`
	Type  string  `json:"type"` // city | troop
	Owner string  `json:"owner"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type GameEvent struct {
	Type string `json:"type"` // battle_result | build_complete | troop_arrived
	Msg  string `json:"msg"`
}

// ── 游戏实体 ──────────────────────────────────────────

type City struct {
	Owner      string
	X, Y       float64
	TowerLevel int
}

type Troop struct {
	ID               string
	Owner            string
	StartX, StartY   float64
	TargetX, TargetY float64
	StartTime        time.Time
	Duration         time.Duration
	Arrived          bool
}

func (t *Troop) Position(now time.Time) (x, y float64) {
	elapsed := now.Sub(t.StartTime)
	ratio := float64(elapsed) / float64(t.Duration)
	if ratio >= 1 {
		return t.TargetX, t.TargetY
	}
	return t.StartX + (t.TargetX-t.StartX)*ratio,
		t.StartY + (t.TargetY-t.StartY)*ratio
}

// ── 战斗 ──────────────────────────────────────────────

type Army struct {
	PlayerID string
	Attack   int
	Defense  int
	Soldiers int
}

type BattleReport struct {
	Winner          string
	Loser           string
	WinnerSurviving int
}

func simulateBattle(atk, def Army) *BattleReport {
	for atk.Soldiers > 0 && def.Soldiers > 0 {
		dmg := (atk.Attack * atk.Soldiers) / (def.Defense + 10)
		if dmg <= 0 {
			dmg = 1
		}
		def.Soldiers -= dmg
		if def.Soldiers <= 0 {
			break
		}
		dmg = (def.Attack * def.Soldiers) / (atk.Defense + 10)
		if dmg <= 0 {
			dmg = 1
		}
		atk.Soldiers -= dmg
	}
	r := &BattleReport{}
	if atk.Soldiers > 0 {
		r.Winner, r.Loser, r.WinnerSurviving = atk.PlayerID, def.PlayerID, atk.Soldiers
	} else {
		r.Winner, r.Loser, r.WinnerSurviving = def.PlayerID, atk.PlayerID, max(def.Soldiers, 0)
	}
	return r
}

// ── World(仅 OnTick goroutine 读写) ──────────────────

type World struct {
	cities map[string]*City
	troops map[string]*Troop
	nextID int

	battleResultCh chan *BattleReport
	buildDoneCh    chan string // player ID
	events         []GameEvent
}

func newWorld() *World {
	return &World{
		cities: map[string]*City{
			"alice": {Owner: "alice", X: 50, Y: 50, TowerLevel: 1},
			"bob":   {Owner: "bob", X: 200, Y: 200, TowerLevel: 1},
			"carol": {Owner: "carol", X: 800, Y: 800, TowerLevel: 1},
		},
		troops:         make(map[string]*Troop),
		battleResultCh: make(chan *BattleReport, 64),
		buildDoneCh:    make(chan string, 64),
	}
}

// ── WorldTick(OnTick 产出,广播给所有订阅者) ──────────

type WorldTick struct {
	Frame    uint64
	Index    *spatial.Index[string]
	Entities map[string]EntityView // id → view
	Events   []GameEvent
}

// ── 主循环逻辑 ────────────────────────────────────────

type MapHandler struct {
	world     *World
	timerQ    *timerqueue.Queue
	battleSch *scheduler.Scheduler
}

func (h *MapHandler) OnTick(frame uint64, inputs []gameloop.PlayerInput[ClientMsg]) []*WorldTick {
	w := h.world
	now := time.Now()
	w.events = w.events[:0]

	// 1. 收集异步结果
	h.drainAsyncResults()

	// 2. 处理玩家指令
	for _, in := range inputs {
		h.handleInput(in, now)
	}

	// 3. 更新行军位置,检测到达
	h.updateTroops(now)

	// 4. 构建空间索引 + 实体快照
	ix := spatial.New[string](cellSize)
	views := make(map[string]EntityView, len(w.cities)+len(w.troops))
	for _, c := range w.cities {
		ix.Add(c.Owner, c.X, c.Y)
		views[c.Owner] = EntityView{ID: c.Owner, Type: "city", Owner: c.Owner, X: c.X, Y: c.Y}
	}
	for _, t := range w.troops {
		x, y := t.Position(now)
		ix.Add(t.ID, x, y)
		views[t.ID] = EntityView{ID: t.ID, Type: "troop", Owner: t.Owner, X: x, Y: y}
	}

	events := make([]GameEvent, len(w.events))
	copy(events, w.events)

	return []*WorldTick{{Frame: frame, Index: ix, Entities: views, Events: events}}
}

func (h *MapHandler) drainAsyncResults() {
	w := h.world
	for {
		select {
		case r := <-w.battleResultCh:
			w.events = append(w.events, GameEvent{
				Type: "battle_result",
				Msg:  fmt.Sprintf("%s 击败 %s,存活 %d 兵", r.Winner, r.Loser, r.WinnerSurviving),
			})
		case player := <-w.buildDoneCh:
			if c, ok := w.cities[player]; ok {
				c.TowerLevel++
				w.events = append(w.events, GameEvent{
					Type: "build_complete",
					Msg:  fmt.Sprintf("%s 塔升至 Lv%d", player, c.TowerLevel),
				})
			}
		default:
			return
		}
	}
}

func (h *MapHandler) handleInput(in gameloop.PlayerInput[ClientMsg], now time.Time) {
	w := h.world
	player := in.Player
	msg := in.Input

	switch msg.Kind {
	case "march":
		city, ok := w.cities[player]
		if !ok {
			return
		}
		dx := msg.TargetX - city.X
		dy := msg.TargetY - city.Y
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < 1 {
			return
		}
		w.nextID++
		id := fmt.Sprintf("troop-%s-%d", player, w.nextID)
		w.troops[id] = &Troop{
			ID: id, Owner: player,
			StartX: city.X, StartY: city.Y,
			TargetX: msg.TargetX, TargetY: msg.TargetY,
			StartTime: now,
			Duration:  time.Duration(dist / troopSpeed * float64(time.Second)),
		}
	case "build":
		if _, ok := w.cities[player]; !ok {
			return
		}
		h.timerQ.Add("build:"+player, buildTime, func() {
			w.buildDoneCh <- player
		})
	}
}

func (h *MapHandler) updateTroops(now time.Time) {
	w := h.world
	var toRemove []string
	for id, t := range w.troops {
		if t.Arrived {
			continue
		}
		elapsed := now.Sub(t.StartTime)
		if elapsed < t.Duration {
			continue
		}
		t.Arrived = true
		w.events = append(w.events, GameEvent{
			Type: "troop_arrived",
			Msg:  fmt.Sprintf("%s 的部队到达 (%.0f,%.0f)", t.Owner, t.TargetX, t.TargetY),
		})

		// 检测目标位置是否有敌方城市 → 提交异步战斗
		for _, c := range w.cities {
			if c.Owner == t.Owner {
				continue
			}
			dx := c.X - t.TargetX
			dy := c.Y - t.TargetY
			if math.Sqrt(dx*dx+dy*dy) < 30 {
				atk := Army{PlayerID: t.Owner, Attack: 15, Defense: 8, Soldiers: 100}
				def := Army{PlayerID: c.Owner, Attack: 10, Defense: 12, Soldiers: 80}
				ch := w.battleResultCh
				h.battleSch.TrySubmit(&scheduler.Task{
					Name: "battle:" + t.Owner + "-vs-" + c.Owner,
					Fn: func(ctx context.Context) error {
						ch <- simulateBattle(atk, def)
						return nil
					},
				})
			}
		}
		toRemove = append(toRemove, id)
	}
	for _, id := range toRemove {
		delete(w.troops, id)
	}
}

// ── main ──────────────────────────────────────────────

func main() {
	world := newWorld()

	timerQ := timerqueue.New(
		timerqueue.WithName("slg-timer"),
		timerqueue.WithResolution(50*time.Millisecond),
	)

	battleSch := scheduler.New(
		scheduler.WithWorkers(4),
		scheduler.WithQueueSize(256),
	)
	battleSch.Start(context.Background())

	handler := &MapHandler{world: world, timerQ: timerQ, battleSch: battleSch}
	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[ClientMsg, *WorldTick](handler.OnTick),
		gameloop.WithName("slg-world"),
	)

	// 每个连接维护自己的 AOI 集合
	aoiSets := sync.Map{}

	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
		player := r.URL.Query().Get("player")
		aoiSet := aoi.NewSet[string]()
		aoiSets.Store(player, aoiSet)
		defer aoiSets.Delete(player)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		ch, unsub := room.Subscribe(ctx)
		defer unsub()

		// 写循环:每帧做 per-player AOI 投影后下发
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case tk, ok := <-ch:
					if !ok {
						return
					}
					px, py, exists := tk.Index.Pos(player)
					if !exists {
						continue
					}
					visible := tk.Index.Nearby(px, py, aoiRadius, player)
					_, _, _ = aoiSet.Diff(visible)
					aoiSet.Update(visible)

					var views []EntityView
					for _, e := range visible {
						if v, ok := tk.Entities[e.ID]; ok {
							views = append(views, v)
						}
					}
					msg := ServerMsg{Frame: tk.Frame, Visible: views, Events: tk.Events}
					if err := c.WriteJSON(ctx, msg); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		// 读循环:解析指令并投入房间
		for {
			var msg ClientMsg
			if err := c.ReadJSON(ctx, &msg); err != nil {
				return err
			}
			room.Push(player, msg)
		}
	}))

	app := beauty.New(
		beauty.WithService(room),
		beauty.WithService(timerQ),
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("slg-world")),
	)

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	<-room.Ready()

	// ── Bot 自动验证 ──────────────────────────────────

	players := []string{"alice", "bob", "carol"}
	botCtx, botCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer botCancel()

	results := make([]*botResult, len(players))
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

	battleSch.Stop()
	verify(results, players)

	cancel()
	<-appErr
}

// ── Bot 客户端 ────────────────────────────────────────

type botResult struct {
	player        string
	sawTroop      bool
	battleEvent   bool
	buildEvent    bool
	arrivedEvent  bool
	maxVisibleIDs int
}

func runBot(ctx context.Context, url, player string) (*botResult, error) {
	c, err := dialRetry(ctx, url+"?player="+player)
	if err != nil {
		return nil, err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	res := &botResult{player: player}

	// alice 发指令:行军攻打 bob + 建筑升级
	if player == "alice" {
		time.Sleep(100 * time.Millisecond)
		_ = wsjson.Write(ctx, c, ClientMsg{Kind: "build"})
		_ = wsjson.Write(ctx, c, ClientMsg{Kind: "march", TargetX: 200, TargetY: 200})
	}

	deadline := time.After(2500 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			return res, nil
		case <-deadline:
			return res, nil
		default:
		}
		readCtx, readCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		var msg ServerMsg
		err := wsjson.Read(readCtx, c, &msg)
		readCancel()
		if err != nil {
			continue
		}

		if len(msg.Visible) > res.maxVisibleIDs {
			res.maxVisibleIDs = len(msg.Visible)
		}
		for _, v := range msg.Visible {
			if v.Type == "troop" {
				res.sawTroop = true
			}
		}
		for _, e := range msg.Events {
			switch e.Type {
			case "battle_result":
				res.battleEvent = true
			case "build_complete":
				res.buildEvent = true
			case "troop_arrived":
				res.arrivedEvent = true
			}
		}
	}
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

// ── 验证报告 ──────────────────────────────────────────

type check struct {
	name string
	ok   bool
}

func verify(results []*botResult, players []string) {
	fmt.Println("──────── SLG 大地图综合验证 ────────")

	alice := findResult(results, "alice")
	bob := findResult(results, "bob")
	carol := findResult(results, "carol")

	checks := []check{
		{"alice 视野内有实体", alice != nil && alice.maxVisibleIDs > 0},
		{"bob 视野内有实体", bob != nil && bob.maxVisibleIDs > 0},
		{"carol 视野无人(距离远)", carol != nil && carol.maxVisibleIDs == 0},
		{"bob 看到 alice 行军部队(AOI)", bob != nil && bob.sawTroop},
		{"部队到达事件", alice != nil && alice.arrivedEvent},
		{"异步战斗结算事件", alice != nil && alice.battleEvent},
		{"建筑升级完成(timerqueue)", alice != nil && alice.buildEvent},
	}

	allOK := true
	for _, c := range checks {
		mark := "✅"
		if !c.ok {
			mark = "❌"
			allOK = false
		}
		fmt.Printf("  %s %s\n", mark, c.name)
	}
	fmt.Println()

	if allOK {
		fmt.Println("结论: ✅ gameloop + spatial/AOI + timerqueue + scheduler 端到端验证通过")
	} else {
		fmt.Println("结论: ⚠️  部分检查未通过(可能是时序问题,重跑一次试试)")
	}

	fmt.Println()
	fmt.Println("架构组成:")
	fmt.Println("  [Bot/WS] → [beauty.WithWebServer] → [gameloop.Room OnTick]")
	fmt.Println("                                         ├─ 行军插值 → spatial.Index.Move → AOI 推送")
	fmt.Println("                                         ├─ 到达检测 → scheduler 异步战斗")
	fmt.Println("                                         └─ 建筑指令 → timerqueue 倒计时 → 完成回调")
	fmt.Println("──────────────────────────────────────")
}

func findResult(results []*botResult, player string) *botResult {
	for _, r := range results {
		if r != nil && r.player == player {
			return r
		}
	}
	return nil
}
