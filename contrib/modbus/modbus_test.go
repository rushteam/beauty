package modbus_test

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/modbus"
	"github.com/rushteam/beauty/pkg/messaging/mq"
)

func TestCollector_String(t *testing.T) {
	c := modbus.NewCollector(nil, nil)
	if got := c.String(); got != "modbus.Collector" {
		t.Fatalf("unexpected String(): %q", got)
	}
}

func TestCollector_WithName(t *testing.T) {
	c := modbus.NewCollector(nil, nil, modbus.WithCollectorName("plc-poller"))
	if got := c.String(); got != "plc-poller" {
		t.Fatalf("unexpected String(): %q", got)
	}
}

func TestCollector_StartStop(t *testing.T) {
	var called int
	handler := func(ctx context.Context, points []modbus.DataPoint) error {
		called++
		return nil
	}

	c := modbus.NewCollector(nil, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestMQBridge(t *testing.T) {
	var published []mq.Message
	fakePub := &fakePublisher{msgs: &published}

	bridge := modbus.MQBridge(fakePub, func(dp modbus.DataPoint) string {
		return "devices/" + dp.DeviceName + "/telemetry"
	})

	points := []modbus.DataPoint{
		{
			DeviceName: "plc-01",
			SlaveID:    1,
			Register:   modbus.RegisterGroup{Type: modbus.HoldingRegister, Start: 0, Quantity: 2},
			Raw:        []byte{0x00, 0x01, 0x00, 0x02},
			Timestamp:  time.Now(),
		},
	}

	err := bridge(context.Background(), points)
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	if len(published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(published))
	}
	if published[0].Topic != "devices/plc-01/telemetry" {
		t.Fatalf("unexpected topic: %s", published[0].Topic)
	}
}

func TestRegisterType_String(t *testing.T) {
	tests := []struct {
		rt   modbus.RegisterType
		want string
	}{
		{modbus.Coil, "coil"},
		{modbus.DiscreteInput, "discrete_input"},
		{modbus.HoldingRegister, "holding_register"},
		{modbus.InputRegister, "input_register"},
		{modbus.RegisterType(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.rt.String(); got != tt.want {
			t.Errorf("RegisterType(%d).String() = %q, want %q", tt.rt, got, tt.want)
		}
	}
}

func TestWriter_New(t *testing.T) {
	w := modbus.NewWriter("192.168.1.100:502", 1, modbus.WithWriterTimeout(2*time.Second))
	if w == nil {
		t.Fatal("NewWriter returned nil")
	}
}

type fakePublisher struct {
	msgs *[]mq.Message
}

func (f *fakePublisher) Publish(_ context.Context, msg mq.Message) error {
	*f.msgs = append(*f.msgs, msg)
	return nil
}
