package rabbitmq

import (
	"fmt"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

func TestFromDelivery(t *testing.T) {
	d := amqp.Delivery{
		RoutingKey:  "order.created",
		Body:        []byte(`{"id":1}`),
		ContentType: "application/json",
		Headers:     amqp.Table{"trace-id": "abc123"},
	}

	msg := fromDelivery(d, "fallback")

	if msg.Topic != "order.created" {
		t.Errorf("expected topic %q, got %q", "order.created", msg.Topic)
	}
	if string(msg.Body) != `{"id":1}` {
		t.Errorf("unexpected body: %s", msg.Body)
	}
	if msg.Headers["content-type"] != "application/json" {
		t.Errorf("expected content-type header")
	}
	if msg.Headers["trace-id"] != "abc123" {
		t.Errorf("expected trace-id header")
	}
}

func TestFromDeliveryFallbackTopic(t *testing.T) {
	d := amqp.Delivery{Body: []byte("hello")}
	msg := fromDelivery(d, "my.topic")
	if msg.Topic != "my.topic" {
		t.Errorf("expected fallback topic, got %q", msg.Topic)
	}
}

func TestIsReconnectable(t *testing.T) {
	if !isReconnectable(amqp.ErrClosed) {
		t.Fatal("expected ErrClosed to be reconnectable")
	}
	err := &amqp.Error{Code: 501, Reason: "channel/connection is not open"}
	if !isReconnectable(err) {
		t.Fatal("expected AMQP 501 to be reconnectable")
	}
	if isReconnectable(fmt.Errorf("validation failed")) {
		t.Fatal("expected validation error not reconnectable")
	}
}

func TestPublisherImplementsInterface(t *testing.T) {
	var _ mq.Publisher = (*Publisher)(nil)
}

func TestSubscriberImplementsInterface(t *testing.T) {
	var _ mq.Subscriber = (*Subscriber)(nil)
}
