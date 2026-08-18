package llmservice

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rushteam/beauty/pkg/mq"
)

// MQConsumer 把 AgentService 接入 pkg/mq:订阅指定 topic,反序列化消息为 Task 投入 worker 池。
// 典型用法:IM/Webhook 3 秒 ACK 后把任务发到 MQ,agent 在后台异步处理。
//
//	consumer := mq.NewConsumer(broker, "agent-tasks")
//	consumer.Handle("agent.run.reviewer", llmservice.MQHandler(svc))
//	app.Start(ctx)
func MQHandler(svc *AgentService) mq.Handler {
	return func(ctx context.Context, msg mq.Message) error {
		var t Task
		if err := json.Unmarshal(msg.Body, &t); err != nil {
			return fmt.Errorf("llmservice: unmarshal task: %w", err)
		}
		return svc.Submit(ctx, t)
	}
}

// PublishTask 是发布侧的辅助:把 Task 序列化后发到 MQ。
func PublishTask(ctx context.Context, pub mq.Publisher, topic string, t Task) error {
	body, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return pub.Publish(ctx, mq.Message{
		Topic: topic,
		Key:   t.RunID,
		Body:  body,
	})
}
