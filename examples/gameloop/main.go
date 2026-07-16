// gameloop lockstep demo:基于 beauty 原语搭一个「帧同步」服务端骨架。
//
// 组成:
//   - gameloop.Room —— 机制型定步长循环(pkg/gameloop),挂进 beauty.WithService;
//   - pkg/ws       —— 每个连接:读循环 Push 输入、写循环把每帧广播写回;
//   - lockstep 策略 —— OnTick 每帧固定广播一帧「本帧全体输入」,让所有客户端按同
//     一帧号推进、拿到同一份输入(确定性重放是客户端游戏逻辑的事,不在框架内)。
//
// 本 demo 自带 3 个进程内 bot:连上 ws、周期发输入、收帧,最后校验「每个客户端
// 逐帧收到的输入完全一致」——这正是框架该保证的 lockstep 传输不变量。
//
// 运行:go run ./examples/gameloop
package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/gameloop"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/ws"
)

const (
	addr     = "127.0.0.1:8123"
	tickRate = 50 * time.Millisecond // 20Hz 逻辑帧
)

// Cmd 是客户端上行的一条输入。
type Cmd struct {
	Seq uint64 `json:"seq"`
	Dir string `json:"dir"`
}

// Frame 是服务端每帧下发的 lockstep 帧:帧号 + 本帧全体输入。
type Frame struct {
	Frame  uint64                      `json:"frame"`
	Inputs []gameloop.PlayerInput[Cmd] `json:"inputs"`
}

func main() {
	// 1) 房间 + lockstep 策略:每帧固定广播一帧(哪怕本帧无输入),保证所有端同步推进。
	room := gameloop.New(tickRate,
		gameloop.HandlerFunc[Cmd, Frame](func(frame uint64, inputs []gameloop.PlayerInput[Cmd]) []Frame {
			return []Frame{{Frame: frame, Inputs: inputs}}
		}),
		gameloop.WithName("demo"),
	)
	var _ beauty.Service = room // 编译期证明:Room 可直接 WithService 挂进框架

	// 2) 连接层:读循环 Push 输入,写循环把每帧广播写回。
	mux := http.NewServeMux()
	mux.Handle("/ws", ws.Handler(func(r *http.Request, c *ws.Conn) error {
		player := r.URL.Query().Get("player")
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		ch, unsub := room.Subscribe(ctx)
		defer unsub()

		// 写循环(单独 goroutine,coder/websocket 允许 1 读 + 1 写并发)。
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case f, ok := <-ch:
					if !ok {
						return
					}
					if err := c.WriteJSON(ctx, f); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		// 读循环:每收到一条输入就投进房间,等下一 tick 聚合。
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
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("gameloop-demo")),
	)

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()

	<-room.Ready() // tick 已在跑

	// 3) 3 个 bot 连上来跑一小会儿,收集各自逐帧收到的输入。
	players := []string{"alice", "bob", "carol"}
	botCtx, botCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer botCancel()

	var wg sync.WaitGroup
	results := make([]map[uint64][]string, len(players))
	for i, p := range players {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			m, err := runBot(botCtx, "ws://"+addr+"/ws", p)
			if err != nil {
				log.Printf("bot %s: %v", p, err)
				return
			}
			results[i] = m
		}(i, p)
	}
	wg.Wait()

	verifyLockstep(players, results)

	cancel() // 停 app(优雅停机:room.Start 退出、连接关闭)
	<-appErr
}

// runBot 连上 ws,周期发输入,后台收帧,返回「帧号 -> 该帧收到的输入标签(player#seq)」。
func runBot(ctx context.Context, url, player string) (map[uint64][]string, error) {
	c, err := dialRetry(ctx, url+"?player="+player)
	if err != nil {
		return nil, err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	frames := map[uint64][]string{}
	var mu sync.Mutex

	go func() {
		for {
			var f Frame
			if err := wsjson.Read(ctx, c, &f); err != nil {
				return
			}
			tags := make([]string, 0, len(f.Inputs))
			for _, in := range f.Inputs {
				tags = append(tags, in.Player+"#"+strconv.FormatUint(in.Input.Seq, 10))
			}
			slices.Sort(tags) // 防御性:同帧内输入顺序无关
			mu.Lock()
			frames[f.Frame] = tags
			mu.Unlock()
		}
	}()

	var seq uint64
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			defer mu.Unlock()
			out := make(map[uint64][]string, len(frames))
			maps.Copy(out, frames)
			return out, nil
		case <-t.C:
			seq++
			_ = wsjson.Write(ctx, c, Cmd{Seq: seq, Dir: "up"})
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

// verifyLockstep 校验 lockstep 传输不变量:所有客户端在其共同帧上收到的输入完全一致。
func verifyLockstep(players []string, results []map[uint64][]string) {
	// 取所有 bot 都收到的帧号(交集)。
	var common []uint64
	base := -1
	for i, m := range results {
		if m == nil {
			log.Printf("客户端 %s 无结果(可能连接失败)", players[i])
			continue
		}
		if base == -1 {
			base = i
			for f := range m {
				common = append(common, f)
			}
			continue
		}
		common = slices.DeleteFunc(common, func(f uint64) bool { _, ok := m[f]; return !ok })
	}
	slices.Sort(common)

	allEqual := true
	withInputs := 0
	var sample []string
	for _, f := range common {
		want := results[base][f]
		if len(want) > 0 {
			withInputs++
		}
		for i, m := range results {
			if m == nil {
				continue
			}
			if !reflect.DeepEqual(m[f], want) {
				allEqual = false
				log.Printf("❌ 帧 %d 输入不一致: %s=%v vs %s=%v", f, players[base], want, players[i], m[f])
			}
		}
		if len(want) > 0 && len(sample) < 3 {
			sample = append(sample, fmt.Sprintf("frame %d -> %v", f, want))
		}
	}

	fmt.Println("──────── lockstep 校验 ────────")
	fmt.Printf("客户端数:            %d\n", len(players))
	fmt.Printf("共同帧数:            %d\n", len(common))
	fmt.Printf("含输入的帧数:        %d\n", withInputs)
	if allEqual {
		fmt.Println("逐帧输入全端一致:    ✅ 是(lockstep 传输不变量成立)")
	} else {
		fmt.Println("逐帧输入全端一致:    ❌ 否")
	}
	for _, s := range sample {
		fmt.Println("  样例:", s)
	}
	fmt.Println("───────────────────────────────")
}
