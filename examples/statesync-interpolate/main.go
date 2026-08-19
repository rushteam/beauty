// statesync-interpolate demo:展示 match(Actor) + replicate(DeltaSync) + interpolate(VisualDelay) 的组合。
//
// 服务器:20Hz 权威仿真(match.Match 的 Actor 模型),每 tick 生成世界快照并广播。
// 客户端:收到快照后存入 interpolate.Buffer,渲染循环以 60fps 从 Buffer
// 中取前后两帧做线性插值,始终落后服务器 100ms(吸收网络抖动)。
//
// 本 demo 进程内同时模拟服务器和 3 个"客户端",每个客户端:
//  1. 收服务器快照 → Push 到 Buffer
//  2. 模拟 60fps 渲染循环 → TimeLine.RenderTime() + Buffer.Bracket() + InterpolateFrame()
//  3. 统计:帧间平滑度(位移方差)
//
// 运行:go run ./examples/statesync-interpolate
package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/game/interpolate"
	"github.com/rushteam/beauty/pkg/game/match"
)

const (
	serverHz    = 20
	tickRate    = time.Second / serverHz
	renderDelay = 100 * time.Millisecond
	renderFPS   = 60
	simDuration = 3 * time.Second
)

// --- 服务器侧:match.Match 做单线程权威仿真 ---

type Pos struct {
	X, Y   float64
	VX, VY float64
}

type State struct {
	Frame    uint64
	Entities map[string]Pos
}

type Input struct {
	Player string
	DX, DY float64
}

type Output struct {
	Frame      uint64
	ServerTime time.Duration
	Entities   map[string]Pos
}

type handler struct{}

func (handler) Init(params map[string]any) (State, int, error) {
	return State{Entities: make(map[string]Pos)}, serverHz, nil
}

func (handler) Tick(_ context.Context, s State, inputs []Input, joins, leaves []match.Presence) (State, []Output, error) {
	s.Frame++
	for _, j := range joins {
		s.Entities[j.UserID] = Pos{X: rand.Float64() * 100, Y: rand.Float64() * 100}
	}
	for _, l := range leaves {
		delete(s.Entities, l.UserID)
	}
	for _, in := range inputs {
		p := s.Entities[in.Player]
		p.VX = in.DX
		p.VY = in.DY
		p.X += p.VX * tickRate.Seconds()
		p.Y += p.VY * tickRate.Seconds()
		s.Entities[in.Player] = p
	}
	out := Output{
		Frame:      s.Frame,
		ServerTime: time.Duration(s.Frame) * tickRate,
		Entities:   make(map[string]Pos, len(s.Entities)),
	}
	for id, p := range s.Entities {
		out.Entities[id] = p
	}
	return s, []Output{out}, nil
}

// --- 客户端侧:模拟网络接收 + interpolate 渲染 ---

type Client struct {
	Player   string
	Buffer   *interpolate.Buffer
	Timeline *interpolate.TimeLine
	epoch    time.Time

	mu            sync.Mutex
	renderSamples []renderSample
	jitterMs      int
}

type renderSample struct {
	X, Y float64
}

func newClient(player string, epoch time.Time, jitterMs int) *Client {
	nowFn := func() time.Time { return time.Now() }
	return &Client{
		Player:   player,
		Buffer:   interpolate.NewBuffer(32),
		Timeline: interpolate.NewTimeLine(interpolate.WithRenderDelay(renderDelay), interpolate.WithNow(nowFn)),
		epoch:    epoch,
		jitterMs: jitterMs,
	}
}

func (c *Client) onServerFrame(out Output) {
	// 模拟网络延迟抖动:随机增加 0~jitterMs 的延迟后处理
	time.Sleep(time.Duration(rand.IntN(c.jitterMs+1)) * time.Millisecond)

	c.Timeline.OnServerFrame(out.ServerTime)

	entities := make([]interpolate.Snapshot, 0, len(out.Entities))
	for id, p := range out.Entities {
		entities = append(entities, interpolate.Snapshot{
			ID: id, X: p.X, Y: p.Y, VX: p.VX, VY: p.VY,
		})
	}
	c.Buffer.Push(interpolate.Frame{ServerTime: out.ServerTime, Entities: entities})
}

func (c *Client) renderFrame() []interpolate.Snapshot {
	rt := c.Timeline.RenderTime()
	if rt <= 0 {
		return nil
	}
	before, after, t, ok := c.Buffer.Bracket(rt)
	if !ok {
		return nil
	}
	return interpolate.InterpolateFrame(before, after, t)
}

func main() {
	fmt.Println("════════ statesync-interpolate: Match(Actor) + Replicate(DeltaSync) + Interpolate(VisualDelay) ════════")
	fmt.Println()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := match.New[State, Input, Output](handler{}, nil, match.WithTickRate(serverHz))
	go m.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	players := []struct {
		Name     string
		JitterMs int
	}{
		{"alice(稳定网络)", 5},
		{"bob(普通网络)", 20},
		{"carol(高抖动)", 50},
	}

	epoch := time.Now()
	clients := make([]*Client, len(players))
	for i, p := range players {
		clients[i] = newClient(p.Name, epoch, p.JitterMs)
		m.QueueJoin(match.Presence{UserID: p.Name})
	}

	// 持续发送输入
	go func() {
		ticker := time.NewTicker(tickRate)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, c := range clients {
					m.QueueInput(Input{
						Player: c.Player,
						DX:     10 + rand.Float64()*5,
						DY:     5 + rand.Float64()*3,
					})
				}
			}
		}
	}()

	// 订阅并转发给各客户端(模拟网络 push)
	subCtx, subCancel := context.WithCancel(ctx)
	ch, unsub := m.Subscribe(subCtx)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for out := range ch {
			for _, c := range clients {
				c := c
				out := out
				go c.onServerFrame(out) // 每客户端独立接收(模拟不同延迟)
			}
		}
	}()

	// 模拟 60fps 客户端渲染循环
	renderTicker := time.NewTicker(time.Second / renderFPS)
	timeout := time.After(simDuration)

renderLoop:
	for {
		select {
		case <-timeout:
			break renderLoop
		case <-renderTicker.C:
			for _, c := range clients {
				snaps := c.renderFrame()
				if snaps == nil {
					continue
				}
				for _, s := range snaps {
					if s.ID == c.Player {
						c.mu.Lock()
						c.renderSamples = append(c.renderSamples, renderSample{X: s.X, Y: s.Y})
						c.mu.Unlock()
					}
				}
			}
		}
	}

	renderTicker.Stop()
	subCancel()
	unsub()
	wg.Wait()
	cancel()

	// 输出统计
	fmt.Printf("  服务器: %dHz tick | 客户端: %dfps render | 渲染延迟: %v\n\n", serverHz, renderFPS, renderDelay)
	fmt.Println("  ┌─────────────────────────┬───────────┬────────────────┬──────────────┐")
	fmt.Println("  │ 玩家                    │ 渲染帧数  │ 帧间平滑度(σ)  │ 结论         │")
	fmt.Println("  ├─────────────────────────┼───────────┼────────────────┼──────────────┤")

	for i, c := range clients {
		c.mu.Lock()
		samples := c.renderSamples
		c.mu.Unlock()

		smoothness := computeSmoothness(samples)
		verdict := "✅ 丝滑"
		if smoothness > 2.0 {
			verdict = "⚠️  轻微卡顿"
		}
		if smoothness > 5.0 {
			verdict = "❌ 有瞬移"
		}

		fmt.Printf("  │ %-22s│ %7d   │ %12.4f   │ %-11s│\n",
			players[i].Name, len(samples), smoothness, verdict)
	}
	fmt.Println("  └─────────────────────────┴───────────┴────────────────┴──────────────┘")
	fmt.Println()
	fmt.Println("  组合架构:")
	fmt.Println("  ┌──────────────────────────────────────────────────────────────────┐")
	fmt.Println("  │  match.Match (Actor Pipeline)                                    │")
	fmt.Println("  │    └─ 单 goroutine 串行化,业务无锁                               │")
	fmt.Println("  │    └─ 每 tick 生成权威世界快照(Output)                            │")
	fmt.Println("  │                     ↓ Subscribe()                                │")
	fmt.Println("  │  replicate.Projector (Delta Sync) — 可在此层做增量裁剪            │")
	fmt.Println("  │    └─ DirtySet + AOI → Spawn/Update/Despawn 增量                 │")
	fmt.Println("  │                     ↓ WebSocket / UDP                            │")
	fmt.Println("  │  interpolate.Buffer + TimeLine (Visual Delay Buffer)             │")
	fmt.Println("  │    └─ 客户端存快照缓冲,渲染落后 100ms                            │")
	fmt.Println("  │    └─ Bracket() + InterpolateFrame() → 60fps 丝滑输出            │")
	fmt.Println("  └──────────────────────────────────────────────────────────────────┘")
}

func computeSmoothness(samples []renderSample) float64 {
	if len(samples) < 3 {
		return 0
	}
	deltas := make([]float64, 0, len(samples)-1)
	for i := 1; i < len(samples); i++ {
		dx := samples[i].X - samples[i-1].X
		dy := samples[i].Y - samples[i-1].Y
		deltas = append(deltas, math.Sqrt(dx*dx+dy*dy))
	}
	mean := 0.0
	for _, d := range deltas {
		mean += d
	}
	mean /= float64(len(deltas))
	variance := 0.0
	for _, d := range deltas {
		diff := d - mean
		variance += diff * diff
	}
	return math.Sqrt(variance / float64(len(deltas)))
}
