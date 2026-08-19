package opcua_test

import (
	"context"
	"testing"
	"time"

	"github.com/gopcua/opcua/ua"

	"github.com/rushteam/beauty/contrib/opcua"
	"github.com/rushteam/beauty/pkg/messaging/mq"
)

func TestClient_String(t *testing.T) {
	c := opcua.NewClient("opc.tcp://localhost:4840")
	want := "opcua.Client(opc.tcp://localhost:4840)"
	if got := c.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestSubscriber_Interface(t *testing.T) {
	var _ mq.Subscriber = (*opcua.Subscriber)(nil)
}

func TestPoller_String(t *testing.T) {
	c := opcua.NewClient("opc.tcp://localhost:4840")
	p := opcua.NewPoller(c, opcua.PollConfig{
		NodeIDs:      []string{"ns=2;s=Temp"},
		PollInterval: time.Second,
	}, func(_ context.Context, _ []opcua.NodeValue) error { return nil })
	if got := p.String(); got != "opcua.Poller" {
		t.Fatalf("String() = %q, want %q", got, "opcua.Poller")
	}
}

func TestOptions(t *testing.T) {
	_ = []opcua.Option{
		opcua.WithSecurityPolicy("Basic256Sha256"),
		opcua.WithSecurityMode(ua.MessageSecurityModeSignAndEncrypt),
		opcua.WithUserPassword("admin", "secret"),
	}
}

func TestMQBridge(t *testing.T) {
	var published []mq.Message
	fakePub := &fakePublisher{msgs: &published}

	handler := opcua.MQBridge(fakePub, func(nv opcua.NodeValue) string {
		return "opcua/" + nv.NodeID
	})

	values := []opcua.NodeValue{
		{NodeID: "ns=2;s=Temp", Value: 23.5, Status: ua.StatusOK, Timestamp: time.Now()},
		{NodeID: "ns=2;s=Pressure", Value: 101.3, Status: ua.StatusOK, Timestamp: time.Now()},
	}

	err := handler(context.Background(), values)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(published) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(published))
	}
	if published[0].Topic != "opcua/ns=2;s=Temp" {
		t.Fatalf("unexpected topic: %s", published[0].Topic)
	}
	if published[0].Key != "ns=2;s=Temp" {
		t.Fatalf("unexpected key: %s", published[0].Key)
	}
}

func TestNewClient_Connect_InvalidEndpoint(t *testing.T) {
	c := opcua.NewClient("opc.tcp://127.0.0.1:19999")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected error connecting to invalid endpoint")
	}
}

type fakePublisher struct {
	msgs *[]mq.Message
}

func (f *fakePublisher) Publish(_ context.Context, msg mq.Message) error {
	*f.msgs = append(*f.msgs, msg)
	return nil
}
