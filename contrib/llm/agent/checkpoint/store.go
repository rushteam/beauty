package checkpoint

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// EventLog 是 append-only checkpoint 事件日志。
type EventLog interface {
	AppendEvents(ctx context.Context, runID string, events ...Event) error
	LoadEvents(ctx context.Context, runID string) ([]Event, error)
	EventCount(ctx context.Context, runID string) (int, error)
}

// MemoryEventLog 是并发安全的内存事件日志。
type MemoryEventLog struct {
	mu     sync.RWMutex
	events map[string][]Event
}

// NewMemoryEventLog 创建内存事件日志。
func NewMemoryEventLog() *MemoryEventLog {
	return &MemoryEventLog{events: map[string][]Event{}}
}

func (l *MemoryEventLog) AppendEvents(_ context.Context, runID string, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	for i := range events {
		if events[i].Timestamp.IsZero() {
			events[i].Timestamp = now
		}
		if events[i].Schema == "" {
			events[i].Schema = SchemaVersion
		}
	}
	l.events[runID] = append(l.events[runID], events...)
	return nil
}

func (l *MemoryEventLog) LoadEvents(_ context.Context, runID string) ([]Event, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	evts, ok := l.events[runID]
	if !ok {
		return nil, nil
	}
	out := make([]Event, len(evts))
	copy(out, evts)
	return out, nil
}

func (l *MemoryEventLog) EventCount(_ context.Context, runID string) (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.events[runID]), nil
}

// DeleteEvents 删除 run 的全部事件(与 RunStore.Delete 联动)。
func (l *MemoryEventLog) DeleteEvents(_ context.Context, runID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.events, runID)
	return nil
}

// ErrNoEvents 表示 run 无事件日志。
var ErrNoEvents = fmt.Errorf("checkpoint: no events")
