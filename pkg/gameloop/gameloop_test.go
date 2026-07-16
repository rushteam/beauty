package gameloop_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/gameloop"
)

// collect 订阅房间,把收到的每个 Out 收进 slice(线程安全),返回读取器与停止函数。
func collect[Out any](t *testing.T, r *gameloop.Room[int, Out]) (get func() []Out, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch, unsub := r.Subscribe(ctx)
	var mu sync.Mutex
	var got []Out
	done := make(chan struct{})
	go func() {
		defer close(done)
		for v := range ch {
			mu.Lock()
			got = append(got, v)
			mu.Unlock()
		}
	}()
	return func() []Out {
			mu.Lock()
			defer mu.Unlock()
			return append([]Out(nil), got...)
		}, func() {
			cancel()
			unsub()
			<-done
		}
}

// runRoom 在后台跑 room.Start,返回停止函数(等 Start 返回)。
func runRoom[In, Out any](t *testing.T, r *gameloop.Room[In, Out]) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()
	select {
	case <-r.Ready():
	case <-time.After(time.Second):
		t.Fatal("room 未在 1s 内就绪")
	}
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Start 返回错误: %v", err)
		}
	}
}

func TestRoom_TickAdvancesFrames(t *testing.T) {
	// 每帧广播当前帧号。
	r := gameloop.New(10*time.Millisecond,
		gameloop.HandlerFunc[int, uint64](func(frame uint64, _ []gameloop.PlayerInput[int]) []uint64 {
			return []uint64{frame}
		}))
	get, stopSub := collect(t, r)
	stopRoom := runRoom(t, r)

	time.Sleep(120 * time.Millisecond)
	stopRoom()
	stopSub()

	frames := get()
	if len(frames) < 5 {
		t.Fatalf("期望至少 5 帧,得到 %d", len(frames))
	}
	// 帧号应严格递增、从 1 起。
	for i, f := range frames {
		if f != uint64(i+1) {
			t.Fatalf("帧号不连续: frames[%d]=%d, 期望 %d", i, f, i+1)
		}
	}
	if r.Frame() < 5 {
		t.Fatalf("Frame()=%d, 期望 >=5", r.Frame())
	}
}

func TestRoom_AggregatesInputsPerTick(t *testing.T) {
	// 每帧回显本帧收集到的输入条数。
	r := gameloop.New(20*time.Millisecond,
		gameloop.HandlerFunc[int, int](func(_ uint64, inputs []gameloop.PlayerInput[int]) []int {
			return []int{len(inputs)}
		}))
	get, stopSub := collect(t, r)
	stopRoom := runRoom(t, r)

	// 一个 tick 内 Push 3 条,应在某一帧一次性被取走(其余帧为 0)。
	r.Push("p1", 1)
	r.Push("p2", 2)
	r.Push("p1", 3)

	time.Sleep(150 * time.Millisecond)
	stopRoom()
	stopSub()

	counts := get()
	total := 0
	maxOne := 0
	for _, c := range counts {
		total += c
		if c > maxOne {
			maxOne = c
		}
	}
	if total != 3 {
		t.Fatalf("输入总数=%d, 期望 3", total)
	}
	if maxOne != 3 {
		t.Fatalf("单帧最大输入数=%d, 期望 3(应在同一 tick 被聚合取走)", maxOne)
	}
}

func TestRoom_FanOutToAllSubscribers(t *testing.T) {
	r := gameloop.New(10*time.Millisecond,
		gameloop.HandlerFunc[int, uint64](func(frame uint64, _ []gameloop.PlayerInput[int]) []uint64 {
			return []uint64{frame}
		}))
	getA, stopA := collect(t, r)
	getB, stopB := collect(t, r)
	stopRoom := runRoom(t, r)

	time.Sleep(80 * time.Millisecond)
	stopRoom()
	stopA()
	stopB()

	a, b := getA(), getB()
	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("两个订阅者都应收到帧: a=%d b=%d", len(a), len(b))
	}
	// 两个订阅者应看到相同的帧号前缀(同一个 Publish 扇出)。
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			t.Fatalf("订阅者收到的帧不一致: a[%d]=%d b[%d]=%d", i, a[i], i, b[i])
		}
	}
}

func TestRoom_GracefulStop(t *testing.T) {
	r := gameloop.New(10*time.Millisecond,
		gameloop.HandlerFunc[int, int](func(_ uint64, _ []gameloop.PlayerInput[int]) []int {
			return []int{0}
		}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()
	<-r.Ready()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start 应无错返回, 得到 %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start 未在 ctx 取消后及时返回")
	}

	// 停机后订阅应立即拿到已关闭的 channel(Broadcaster 已 Close)。
	ch, unsub := r.Subscribe(context.Background())
	defer unsub()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("停机后不应再有新值")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("停机后订阅 channel 应已关闭")
	}
}
