// mq demo:消息队列抽象 + 「消费者即 Service」。用零依赖的进程内 broker(NewInProc)演示
// 发布/订阅、队列组竞争消费、处理中间件(Recover/Retry),消费者作为 beauty.Service 随 app 停机。
//
// 换真 broker:把 mq.NewInProc() 换成实现了 mq.Publisher/mq.Subscriber 的 broker 绑定
// (如未来的 pkg/infra/nats),其余代码不动。
//
// 运行:
//
//	go run ./examples/mq
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/mq"
)

func main() {
	broker := mq.NewInProc()

	// 消费者:处理 orders 主题;用队列组 order-workers(多副本时组内竞争消费),
	// 处理链套 Recover(吞 panic)+ Retry(瞬时错误重试 3 次)。
	consumer := mq.NewConsumer(broker, mq.WithConsumerName("orders")).
		Handle("orders",
			mq.Chain(handleOrder, mq.Recover(), mq.Retry(3, 100*time.Millisecond)),
			mq.WithGroup("order-workers"),
		)

	// 生产者:等消费者就绪后发几条(真实场景由 HTTP/gRPC handler 等触发 Publish)。
	go func() {
		<-consumer.Ready()
		for i := range 5 {
			_ = broker.Publish(context.Background(), mq.Message{
				Topic:   "orders",
				Key:     fmt.Sprintf("user-%d", i%2), // 分区键(有序/分区用)
				Body:    []byte(fmt.Sprintf("order-%d", i)),
				Headers: map[string]string{"content-type": "text/plain"},
			})
			time.Sleep(300 * time.Millisecond)
		}
	}()

	app := beauty.New(beauty.WithService(consumer))
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func handleOrder(_ context.Context, m mq.Message) error {
	slog.Info("处理订单", "topic", m.Topic, "key", m.Key, "body", string(m.Body))
	return nil
}
