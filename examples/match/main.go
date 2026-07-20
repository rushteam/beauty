// match 示例:一个 20Hz tick 的 echo 房间。
//
// 多个 WebSocket 客户端连入,每帧把本帧收到的输入打包成一条广播消息发给所有人,
// 演示 pkg/match 的:固定帧率、批量输入消费、产出扇出、背压降级、空闲停止。
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/match"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/ws"
)

// 房间状态:累计收到的输入总数。
type roomState struct {
	Total int
}

// 输入:客户端发来的数字。
type roomInput struct {
	From int `json:"from"`
	Val  int `json:"val"`
}

// 产出:每帧广播给所有订阅者。
type roomOutput struct {
	Tick   int         `json:"tick"`
	Total  int         `json:"total"`
	Inputs []roomInput `json:"inputs"`
}

type roomHandler struct{}

func (roomHandler) Init(params map[string]any) (roomState, int, error) {
	return roomState{}, 20, nil // 20 Hz
}

func (roomHandler) Tick(ctx context.Context, state roomState, inputs []roomInput, join []match.Presence, leave []match.Presence) (roomState, []roomOutput, error) {
	state.Total += len(inputs)
	out := roomOutput{Tick: state.Total, Total: state.Total, Inputs: inputs}
	if len(inputs) == 0 && len(join) == 0 && len(leave) == 0 {
		return state, nil, nil // 空帧不广播
	}
	return state, []roomOutput{out}, nil
}

func main() {
	m := match.New[roomState, roomInput, roomOutput](
		roomHandler{}, nil,
		match.WithTickRate(20),
		match.WithInputQueue(256),
		match.WithMaxIdleSec(60),
	)
	if err := m.Start(context.Background()); err != nil {
		log.Fatalf("match start: %v", err)
	}
	defer func() {
		m.Stop()
		_ = m.Wait()
	}()

	mux := http.NewServeMux()

	// WebSocket:客户端连入即订阅房间广播;收到消息作为输入投递给 match。
	// WithInsecureSkipVerify 仅用于本地 demo,生产应配置 WithOriginPatterns。
	mux.Handle("/room", ws.Handler(func(r *http.Request, c *ws.Conn) error {
		ctx := r.Context()
		out, cancel := m.Subscribe(ctx)
		defer cancel()

		// 读循环:把客户端 JSON 投递给 match。
		go func() {
			for {
				var in roomInput
				if err := c.ReadJSON(ctx, &in); err != nil {
					return
				}
				if !m.QueueInput(in) {
					log.Printf("input dropped (queue full)")
				}
			}
		}()

		// 写循环:把 match 产出广播给本客户端。
		for {
			select {
			case <-ctx.Done():
				return nil
			case o, ok := <-out:
				if !ok {
					return nil
				}
				if err := c.WriteJSON(ctx, o); err != nil {
					return err
				}
			}
		}
	}, ws.WithInsecureSkipVerify()))

	// 健康检查 + 状态。
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tick":      m.TickCount(),
			"rate":      m.Rate(),
			"stopped":   m.Stopped(),
			"tick_rate": strconv.Itoa(m.Rate()) + "Hz",
		})
	})

	app := beauty.New(beauty.WithWebServer(":8181", mux, webserver.WithServiceName("match-demo")))
	log.Println("match demo on :8181  (ws://localhost:8181/room, http://localhost:8181/status)")
	if err := app.Start(context.Background()); err != nil {
		log.Fatalf("app: %v", err)
	}
}
