package redisstream

import (
	"encoding/json"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

func TestFromXMessage(t *testing.T) {
	xmsg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]interface{}{
			"body":    `{"order_id":"abc"}`,
			"key":     "order.abc",
			"headers": `{"trace-id":"xyz","content-type":"application/json"}`,
		},
	}

	msg := fromXMessage(xmsg, "orders")

	if msg.Topic != "orders" {
		t.Errorf("expected topic %q, got %q", "orders", msg.Topic)
	}
	if string(msg.Body) != `{"order_id":"abc"}` {
		t.Errorf("unexpected body: %s", msg.Body)
	}
	if msg.Key != "order.abc" {
		t.Errorf("expected key %q, got %q", "order.abc", msg.Key)
	}
	if msg.Headers["trace-id"] != "xyz" {
		t.Errorf("expected trace-id header")
	}
	if msg.Headers["content-type"] != "application/json" {
		t.Errorf("expected content-type header")
	}
}

func TestFromXMessageMinimal(t *testing.T) {
	xmsg := redis.XMessage{
		ID:     "1-0",
		Values: map[string]interface{}{"body": "hello"},
	}
	msg := fromXMessage(xmsg, "stream1")
	if msg.Topic != "stream1" {
		t.Errorf("expected topic stream1, got %q", msg.Topic)
	}
	if string(msg.Body) != "hello" {
		t.Errorf("unexpected body: %s", msg.Body)
	}
	if msg.Key != "" {
		t.Errorf("expected empty key, got %q", msg.Key)
	}
	if msg.Headers != nil {
		t.Errorf("expected nil headers, got %v", msg.Headers)
	}
}

func TestToXAddValues(t *testing.T) {
	msg := mq.Message{
		Topic:   "events",
		Key:     "user.123",
		Body:    []byte(`{"type":"login"}`),
		Headers: map[string]string{"trace-id": "t1"},
	}

	values := ToXAddValues(msg)

	if values["body"] != `{"type":"login"}` {
		t.Errorf("unexpected body value: %v", values["body"])
	}
	if values["key"] != "user.123" {
		t.Errorf("unexpected key value: %v", values["key"])
	}

	var headers map[string]string
	if err := json.Unmarshal([]byte(values["headers"].(string)), &headers); err != nil {
		t.Fatalf("failed to unmarshal headers: %v", err)
	}
	if headers["trace-id"] != "t1" {
		t.Errorf("expected trace-id in headers")
	}
}

func TestPublisherImplementsInterface(t *testing.T) {
	var _ mq.Publisher = (*Publisher)(nil)
}

func TestSubscriberImplementsInterface(t *testing.T) {
	var _ mq.Subscriber = (*Subscriber)(nil)
}
