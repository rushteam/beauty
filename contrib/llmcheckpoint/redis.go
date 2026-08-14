package llmcheckpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// RedisStore 用 Redis STRING 存 RunSnapshot 与事件 JSON。
type RedisStore struct {
	rdb    redis.UniversalClient
	prefix string
	ttl    time.Duration
}

// RedisOption 配置 RedisStore。
type RedisOption func(*RedisStore)

// WithKeyPrefix 设置 key 前缀(默认 "beauty:llm:checkpoint:")。
func WithKeyPrefix(p string) RedisOption {
	return func(s *RedisStore) { s.prefix = p }
}

// WithTTL 设置 TTL(<=0 不过期)。
func WithTTL(d time.Duration) RedisOption {
	return func(s *RedisStore) { s.ttl = d }
}

// NewRedis 用已有 go-redis 客户端创建 CheckpointStore。
func NewRedis(rdb redis.UniversalClient, opts ...RedisOption) (*RedisStore, error) {
	if rdb == nil {
		return nil, fmt.Errorf("llmcheckpoint: nil redis client")
	}
	s := &RedisStore{rdb: rdb, prefix: "beauty:llm:checkpoint:"}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *RedisStore) snapKey(id string) (string, error) {
	safe, err := sanitizeRunID(id)
	if err != nil {
		return "", err
	}
	return s.prefix + safe, nil
}

func (s *RedisStore) eventsKey(id string) (string, error) {
	safe, err := sanitizeRunID(id)
	if err != nil {
		return "", err
	}
	return s.prefix + safe + ":events", nil
}

func sanitizeRunID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("llmcheckpoint: empty run id")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			return "", fmt.Errorf("llmcheckpoint: invalid run id %q", id)
		}
	}
	if strings.Contains(id, "..") {
		return "", fmt.Errorf("llmcheckpoint: invalid run id %q", id)
	}
	return id, nil
}

// Save 实现 agent.RunStore。
func (s *RedisStore) Save(ctx context.Context, id string, snap *agent.RunSnapshot) error {
	if id == "" || snap == nil {
		return fmt.Errorf("llmcheckpoint: Save requires id and snapshot")
	}
	key, err := s.snapKey(id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, raw, s.ttl).Err()
}

// Load 实现 agent.RunStore。
func (s *RedisStore) Load(ctx context.Context, id string) (*agent.RunSnapshot, error) {
	key, err := s.snapKey(id)
	if err != nil {
		return nil, err
	}
	b, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap agent.RunSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, fmt.Errorf("llmcheckpoint: decode snapshot: %w", err)
	}
	return &snap, nil
}

// Delete 删除暂停快照;事件日志保留。
func (s *RedisStore) Delete(ctx context.Context, id string) error {
	key, err := s.snapKey(id)
	if err != nil {
		return err
	}
	return s.rdb.Del(ctx, key).Err()
}

// AppendEvents 实现 checkpoint.EventLog。
func (s *RedisStore) AppendEvents(ctx context.Context, runID string, events ...checkpoint.Event) error {
	if len(events) == 0 {
		return nil
	}
	key, err := s.eventsKey(runID)
	if err != nil {
		return err
	}
	var prev []checkpoint.Event
	b, err := s.rdb.Get(ctx, key).Bytes()
	if err == nil {
		_ = json.Unmarshal(b, &prev)
	} else if err != redis.Nil {
		return err
	}
	prev = append(prev, events...)
	raw, err := json.Marshal(prev)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, raw, s.ttl).Err()
}

// LoadEvents 实现 checkpoint.EventLog。
func (s *RedisStore) LoadEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	key, err := s.eventsKey(runID)
	if err != nil {
		return nil, err
	}
	b, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []checkpoint.Event
	if err := json.Unmarshal(b, &events); err != nil {
		return nil, fmt.Errorf("llmcheckpoint: decode events: %w", err)
	}
	return events, nil
}

// EventCount 实现 checkpoint.EventLog。
func (s *RedisStore) EventCount(ctx context.Context, runID string) (int, error) {
	events, err := s.LoadEvents(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(events), nil
}

var _ agent.CheckpointStore = (*RedisStore)(nil)
