// Webhook 示例：注册端点（事件过滤 + 签名），按事件触发通知。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/webhook"
)

func main() {
	n := webhook.New(
		webhook.WithRetries(3),
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
	n.Notify(context.Background(), webhook.Event{Type: "order.created", Payload: order{"A-1"}}) // 不匹配
	n.Notify(context.Background(), webhook.Event{Type: "order.paid", Payload: order{"A-2"}})    // 匹配 → 触发

	time.Sleep(time.Second) // 等异步投递（示例用）
	fmt.Println("done")
}
