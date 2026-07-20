// Webhook 示例：注册端点（事件过滤 + 签名 + 幂等去重 + DLQ），按事件触发通知。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/webhook"
)

func main() {
	store := webhook.NewMemStore()
	dlq := webhook.NewMemDLQ()
	n := webhook.New(
		webhook.WithRetries(3),
		webhook.WithStore(store), // 启用幂等去重 + 投递状态追踪
		webhook.WithDLQ(dlq),     // 启用死信队列:重试耗尽后入队,可 Replay
		webhook.WithErrorHandler(func(ep webhook.Endpoint, ev webhook.Event, err error) {
			fmt.Printf("webhook failed: url=%s event=%s err=%v\n", ep.URL, ev.Type, err)
		}),
	)

	// 仅接收 order.paid 事件，带 HMAC 签名与自定义 body 模板
	_ = n.Add(webhook.Endpoint{
		URL:          "https://example.com/hooks/orders",
		Events:       []string{"order.paid"},
		Secret:       "whsec_demo",
		Headers:      map[string]string{"X-Source": "beauty"},
		BodyTemplate: `{"type":"{{.Type}}","order":"{{.Payload.OrderID}}"}`,
	})

	type order struct{ OrderID string }
	// 同一 EventID 投两次:幂等去重,只投一次。
	n.Notify(context.Background(), webhook.Event{Type: "order.paid", EventID: "evt-1", Payload: order{"A-2"}})
	n.Notify(context.Background(), webhook.Event{Type: "order.paid", EventID: "evt-1", Payload: order{"A-2"}})

	time.Sleep(time.Second) // 等异步投递(示例用)
	fmt.Printf("delivery records: %d\n", len(store.Records()))
	fmt.Printf("dead letters: %d\n", dlq.Len())
	// 失败的投递可重放:
	// ok, err := n.Replay(context.Background())
	fmt.Println("done")
}
