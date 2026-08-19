package mqtt_test

import (
	"context"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/rushteam/beauty/contrib/mqtt"
	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// TestClient_Interface 验证接口满足编译时类型检查。
func TestClient_Interface(t *testing.T) {
	// 仅编译时检查,不执行(需要 broker)
	var _ mq.Publisher = (*mqtt.Client)(nil)
	var _ mq.Subscriber = (*mqtt.Client)(nil)
}

// TestConnect_InvalidBroker 验证连接失败返回错误。
func TestConnect_InvalidBroker(t *testing.T) {
	// 使用不存在的地址,应快速返回错误
	opts := pahomqtt.NewClientOptions()
	opts.SetConnectTimeout(200 * time.Millisecond)

	_, err := mqtt.Connect("tcp://127.0.0.1:19999",
		mqtt.WithClientID("test-invalid"),
		mqtt.WithPahoOptions(func(o *pahomqtt.ClientOptions) {
			o.SetConnectTimeout(200 * time.Millisecond)
		}),
	)
	if err == nil {
		t.Fatal("expected error connecting to invalid broker")
	}
}

// TestOptions 验证选项配置不会 panic。
func TestOptions(t *testing.T) {
	// 仅验证选项函数不 panic,不实际连接
	_ = []mqtt.Option{
		mqtt.WithClientID("dev-001"),
		mqtt.WithCredentials("user", "pass"),
		mqtt.WithQoS(1),
		mqtt.WithCleanSession(false),
		mqtt.WithKeepAlive(30 * time.Second),
		mqtt.WithAutoReconnect(true),
		mqtt.WithWill("devices/dev-001/status", []byte("offline"), 1, true),
	}
}

// integration test — 需要本地 MQTT broker 运行时才能通过
// 运行: MQTT_BROKER=tcp://127.0.0.1:1883 go test -run TestIntegration -v
func TestIntegration_PubSub(t *testing.T) {
	broker := "tcp://127.0.0.1:1883"
	// 尝试连接,失败则跳过(没有 broker)
	client, err := mqtt.Connect(broker,
		mqtt.WithClientID("beauty-test"),
		mqtt.WithQoS(1),
		mqtt.WithPahoOptions(func(o *pahomqtt.ClientOptions) {
			o.SetConnectTimeout(500 * time.Millisecond)
		}),
	)
	if err != nil {
		t.Skipf("skipping integration test, no MQTT broker at %s: %v", broker, err)
	}
	defer client.Close()

	received := make(chan mq.Message, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = client.Subscribe(ctx, "test/beauty/echo", func(_ context.Context, msg mq.Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	err = client.Publish(ctx, mq.Message{
		Topic: "test/beauty/echo",
		Body:  []byte("hello-iot"),
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-received:
		if string(msg.Body) != "hello-iot" {
			t.Fatalf("unexpected body: %q", msg.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}
