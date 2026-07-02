package eventbus_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/eventbus"
)

type userEvent struct {
	User string
}

func TestPublishSubscribe(t *testing.T) {
	bus := eventbus.New[userEvent]()
	var got []string
	bus.Subscribe("login", func(topic string, e userEvent) {
		got = append(got, e.User)
	})
	n := bus.Publish("login", userEvent{User: "alice"})
	if n != 1 {
		t.Fatalf("notified = %d, want 1", n)
	}
	if len(got) != 1 || got[0] != "alice" {
		t.Fatalf("got = %v", got)
	}
}

func TestTopicIsolation(t *testing.T) {
	bus := eventbus.New[userEvent]()
	var login, logout int
	bus.Subscribe("login", func(string, userEvent) { login++ })
	bus.Subscribe("logout", func(string, userEvent) { logout++ })

	bus.Publish("login", userEvent{})
	bus.Publish("login", userEvent{})
	bus.Publish("logout", userEvent{})

	if login != 2 || logout != 1 {
		t.Fatalf("login=%d logout=%d, want 2/1", login, logout)
	}
}

func TestMultipleSubscribersSameTopic(t *testing.T) {
	bus := eventbus.New[int]()
	var a, b int
	bus.Subscribe("t", func(string, int) { a++ })
	bus.Subscribe("t", func(string, int) { b++ })
	bus.Publish("t", 1)
	if a != 1 || b != 1 {
		t.Fatalf("both handlers should fire: a=%d b=%d", a, b)
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := eventbus.New[int]()
	var n int
	unsub := bus.Subscribe("t", func(string, int) { n++ })
	bus.Publish("t", 1)
	unsub()
	bus.Publish("t", 1)
	if n != 1 {
		t.Fatalf("after unsubscribe should not fire: n=%d", n)
	}
	if bus.SubscriberCount("t") != 0 {
		t.Fatalf("subscriber count = %d, want 0", bus.SubscriberCount("t"))
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	bus := eventbus.New[int]()
	unsub := bus.Subscribe("t", func(string, int) {})
	unsub()
	unsub() // 不应 panic 或误删他人
	bus.Subscribe("t", func(string, int) {})
	if bus.SubscriberCount("t") != 1 {
		t.Fatalf("count = %d, want 1", bus.SubscriberCount("t"))
	}
}

func TestPublishNoSubscribers(t *testing.T) {
	bus := eventbus.New[int]()
	if n := bus.Publish("nobody", 1); n != 0 {
		t.Fatalf("publish to empty topic = %d, want 0", n)
	}
}

func TestSyncPanicRecovered(t *testing.T) {
	var gotPanic atomic.Bool
	bus := eventbus.New[int](eventbus.WithOnPanic(func(topic string, err error) {
		gotPanic.Store(true)
	}))
	var after int
	bus.Subscribe("t", func(string, int) { panic("boom") })
	bus.Subscribe("t", func(string, int) { after++ })

	// panic 被恢复,不影响后续 handler 与调用方。
	bus.Publish("t", 1)
	if !gotPanic.Load() {
		t.Fatal("onPanic not called")
	}
	if after != 1 {
		t.Fatal("later handler should still run after earlier panic")
	}
}

func TestAsyncDispatch(t *testing.T) {
	bus := eventbus.New[int](eventbus.WithAsync(true))
	var wg sync.WaitGroup
	wg.Add(1)
	var got atomic.Int64
	bus.Subscribe("t", func(string, int) {
		got.Add(1)
		wg.Done()
	})
	bus.Publish("t", 1) // 异步:不阻塞
	wg.Wait()
	if got.Load() != 1 {
		t.Fatalf("async handler = %d, want 1", got.Load())
	}
}

func TestConcurrentPubSub(t *testing.T) {
	bus := eventbus.New[int]()
	var total atomic.Int64
	// 固定订阅者
	for range 10 {
		bus.Subscribe("hot", func(string, int) { total.Add(1) })
	}
	var wg sync.WaitGroup
	// 并发发布 + 并发订阅/退订
	for range 50 {
		wg.Go(func() {
			for range 100 {
				bus.Publish("hot", 1)
			}
		})
	}
	for range 20 {
		wg.Go(func() {
			unsub := bus.Subscribe("hot", func(string, int) {})
			time.Sleep(time.Millisecond)
			unsub()
		})
	}
	wg.Wait()
	// 固定 10 个订阅者 × 5000 次发布 = 50000(动态订阅者的计入不确定,故只验下界)。
	if total.Load() < 50000 {
		t.Fatalf("total = %d, want >= 50000", total.Load())
	}
}
