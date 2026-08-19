package modbus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// MQBridge 将 Modbus 采集数据桥接到 mq.Publisher(遥测上云)。
// topicFn 根据 DataPoint 生成目标 topic(如 "devices/modbus/{name}/telemetry")。
func MQBridge(pub mq.Publisher, topicFn func(DataPoint) string) Handler {
	return func(ctx context.Context, points []DataPoint) error {
		for _, p := range points {
			body, err := json.Marshal(bridgePayload{
				DeviceName: p.DeviceName,
				SlaveID:    p.SlaveID,
				Register:   p.Register.Type.String(),
				Start:      p.Register.Start,
				Quantity:   p.Register.Quantity,
				Raw:        p.Raw,
				Timestamp:  p.Timestamp.UnixMilli(),
			})
			if err != nil {
				return fmt.Errorf("modbus: marshal: %w", err)
			}
			msg := mq.Message{
				Topic: topicFn(p),
				Key:   fmt.Sprintf("%s-slave-%d", p.DeviceName, p.SlaveID),
				Body:  body,
			}
			if err := pub.Publish(ctx, msg); err != nil {
				return fmt.Errorf("modbus: publish: %w", err)
			}
		}
		return nil
	}
}

type bridgePayload struct {
	DeviceName string `json:"device_name"`
	SlaveID    byte   `json:"slave_id"`
	Register   string `json:"register"`
	Start      uint16 `json:"start"`
	Quantity   uint16 `json:"quantity"`
	Raw        []byte `json:"raw"`
	Timestamp  int64  `json:"timestamp"`
}
