package console

import (
	"context"
	"sync"
	"time"
)

// Topic 是一个周期性数据推送主题。客户端订阅后,服务端按 Interval 调用 Build
// 生成内容并推送给所有订阅者。适合监控类数据(在线数、内存、系统负载等)。
type Topic struct {
	// Name 是主题名(订阅时使用)。
	Name string
	// Note 是说明,用于 topics 命令列表展示。
	Note string
	// Interval 是推送周期,必须 > 0。
	Interval time.Duration
	// Build 生成一次推送的文本内容,不可为空。
	Build func() string

	mu   sync.Mutex
	subs map[*client]struct{}
}

func (t *Topic) addSub(cl *client) {
	t.mu.Lock()
	if t.subs == nil {
		t.subs = make(map[*client]struct{})
	}
	t.subs[cl] = struct{}{}
	t.mu.Unlock()
}

func (t *Topic) removeSub(cl *client) {
	t.mu.Lock()
	delete(t.subs, cl)
	t.mu.Unlock()
}

func (t *Topic) snapshotSubs() []*client {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.subs) == 0 {
		return nil
	}
	out := make([]*client, 0, len(t.subs))
	for cl := range t.subs {
		out = append(out, cl)
	}
	return out
}

// run 是主题的推送循环,ctx 取消时退出。
func (t *Topic) run(ctx context.Context) {
	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			subs := t.snapshotSubs()
			if len(subs) == 0 {
				continue
			}
			data := t.Build()
			for _, cl := range subs {
				_ = cl.write(ctx, response{OK: true, Type: "push", Push: t.Name, Data: data})
			}
		}
	}
}
