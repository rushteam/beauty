package match

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// echoHandler 每帧把累积输入转成产出,用于测试 tick/输入/扇出。
type echoHandler struct {
	rate int
}

func (echoHandler) Init(params map[string]any) (int, int, error) {
	rate := 100 // 高帧率让测试快速推进
	if v, ok := params["rate"].(int); ok && v > 0 {
		rate = v
	}
	return 0, rate, nil
}

func (echoHandler) Tick(ctx context.Context, state int, inputs []int, join []Presence, leave []Presence) (int, []int, error) {
	return state + len(inputs), inputs, nil
}

func TestMatch_TickAndFanout(t *testing.T) {
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100), WithSubBufferSize(16))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	ch, cancel := m.Subscribe(context.Background())
	defer cancel()

	// 投递几条输入,应被 tick 扇出。
	for i := 0; i < 3; i++ {
		if !m.QueueInput(i) {
			t.Fatalf("QueueInput %d dropped", i)
		}
	}

	got := make([]int, 0, 3)
	for {
		select {
		case v := <-ch:
			got = append(got, v)
			if len(got) == 3 {
				return
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout, got %v", got)
		}
	}
}

func TestMatch_Signal(t *testing.T) {
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	var seen atomic.Int32
	done := make(chan struct{})
	ok := m.Signal(func(s int) {
		seen.Add(1)
		close(done)
	})
	if !ok {
		t.Fatal("Signal returned false")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Signal not executed")
	}
	if seen.Load() != 1 {
		t.Fatalf("Signal executed %d times", seen.Load())
	}
}

func TestMatch_BackpressureDropsInput(t *testing.T) {
	// 容量1 + 高帧率,投递超过队列的输入应被丢弃而非阻塞。
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100), WithInputQueue(1))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	// 不订阅也不读取,让 inputCh 堆满。
	m.QueueInput(1)
	m.QueueInput(2) // 应被丢弃
}

func TestMatch_IdleStop(t *testing.T) {
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100), WithMaxIdleSec(1))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-m.done:
		// 空闲超时后自动停止
	case <-time.After(3 * time.Second):
		t.Fatal("idle stop did not trigger")
	}
	if !m.Stopped() {
		t.Fatal("should be stopped")
	}
}

func TestMatch_StopIdempotent(t *testing.T) {
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.Stop()
	m.Stop() // 重复 Stop 不 panic
	if err := m.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestMatch_StopClosesSubscribers(t *testing.T) {
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ch, cancel := m.Subscribe(context.Background())
	defer cancel()
	m.Stop()

	// Stop 后订阅 channel 应被关闭。
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel not closed after Stop")
		}
	case <-time.After(time.Second):
		t.Fatal("channel close not detected")
	}
}

func TestMatch_InitError(t *testing.T) {
	m := New[int, int, int](errHandler{}, nil)
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("expected Init error")
	}
	if err := m.Wait(); err != nil {
		t.Fatalf("Wait after Init error: %v", err)
	}
}

type errHandler struct{}

func (errHandler) Init(params map[string]any) (int, int, error) {
	return 0, 0, context.Canceled
}
func (errHandler) Tick(ctx context.Context, state int, inputs []int, join []Presence, leave []Presence) (int, []int, error) {
	return 0, nil, nil
}

func TestMatch_TickErrorStops(t *testing.T) {
	m := New[int, int, int](tickErrHandler{}, nil, WithTickRate(100))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Wait(); err == nil {
		t.Fatal("expected Tick error")
	}
	if !m.Stopped() {
		t.Fatal("should be stopped after Tick error")
	}
}

type tickErrHandler struct{}

func (tickErrHandler) Init(params map[string]any) (int, int, error) { return 0, 100, nil }
func (tickErrHandler) Tick(ctx context.Context, state int, inputs []int, join []Presence, leave []Presence) (int, []int, error) {
	return 0, nil, context.DeadlineExceeded
}

func TestMatch_JoinLeaveBatched(t *testing.T) {
	h := &joinLeaveHandler{}
	m := New[int, presenceEvent, int](h, nil, WithTickRate(100), WithSubBufferSize(16))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	m.QueueJoin(Presence{UserID: "a"})
	m.QueueJoin(Presence{UserID: "b"})
	m.QueueLeave(Presence{UserID: "a"})

	// 等待至少一帧处理完。
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("joins=%d leaves=%d", h.joins.Load(), h.leaves.Load())
		default:
		}
		if h.joins.Load() == 2 && h.leaves.Load() == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

type presenceEvent struct{ id string }

type joinLeaveHandler struct {
	joins  atomic.Int32
	leaves atomic.Int32
}

func (*joinLeaveHandler) Init(params map[string]any) (int, int, error) { return 0, 100, nil }
func (h *joinLeaveHandler) Tick(ctx context.Context, state int, inputs []presenceEvent, join []Presence, leave []Presence) (int, []int, error) {
	h.joins.Add(int32(len(join)))
	h.leaves.Add(int32(len(leave)))
	return state, nil, nil
}

func TestMatch_ConcurrentQueueInput(t *testing.T) {
	m := New[int, int, int](echoHandler{}, nil, WithTickRate(100), WithInputQueue(1024))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer m.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.QueueInput(j)
			}
		}()
	}
	wg.Wait() // 不 panic、不死锁即通过
}
