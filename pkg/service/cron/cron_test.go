package cron_test

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/cron"
)

func TestCron_AddRemoveBeforeStart(t *testing.T) {
	c := cron.New(cron.WithCronHandler("@every 1h", func(context.Context) error { return nil }, cron.HandlerName("static")))
	if err := c.Add("not a spec", func(context.Context) error { return nil }); err == nil {
		t.Fatal("非法 spec 应报错")
	}
	if err := c.Add("@every 1h", func(context.Context) error { return nil }, cron.HandlerName("dyn")); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("@every 2h", func(context.Context) error { return nil }, cron.HandlerName("dyn")); err != nil {
		t.Fatal(err)
	}
	if got := c.Names(); !slices.Equal(got, []string{"static", "dyn"}) {
		t.Fatalf("names = %v", got)
	}
	if !c.Remove("static") || c.Remove("static") {
		t.Fatal("Remove 返回值不对")
	}
	if got := c.Names(); !slices.Equal(got, []string{"dyn"}) {
		t.Fatalf("names = %v", got)
	}
}

func TestCron_AddRemoveAfterStart(t *testing.T) {
	c := cron.New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Start(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	var hits atomic.Int32
	fired := make(chan struct{}, 8)
	// Start 是异步的,轮询直到运行期 Add 生效(Entries 出现)。
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := c.Add("@every 1s", func(context.Context) error {
			hits.Add(1)
			fired <- struct{}{}
			return nil
		}, cron.HandlerName("tick")); err != nil {
			t.Fatal(err)
		}
		if len(c.Entries()) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("运行期 Add 未进入调度表")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 同名重复 Add 是替换,不会叠加
	if err := c.Add("@every 1s", func(context.Context) error {
		hits.Add(1)
		fired <- struct{}{}
		return nil
	}, cron.HandlerName("tick")); err != nil {
		t.Fatal(err)
	}
	if n := len(c.Entries()); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}

	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("任务未触发")
	}
	if !c.Remove("tick") {
		t.Fatal("Remove 应返回 true")
	}
	if n := len(c.Entries()); n != 0 {
		t.Fatalf("entries = %d, want 0", n)
	}
}
