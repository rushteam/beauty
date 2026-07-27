package llmsession

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
)

// RedisStore 用 Redis STRING 存会话 JSON。key = prefix + id。
type RedisStore struct {
	rdb    redis.UniversalClient
	prefix string
	ttl    time.Duration // 0=不过期
}

// RedisOption 配置 RedisStore。
type RedisOption func(*RedisStore)

// WithKeyPrefix 设置 key 前缀(默认 "beauty:llm:session:")。
func WithKeyPrefix(p string) RedisOption {
	return func(s *RedisStore) { s.prefix = p }
}

// WithTTL 设置会话 TTL(<=0 不过期)。
func WithTTL(d time.Duration) RedisOption {
	return func(s *RedisStore) { s.ttl = d }
}

// NewRedis 用已有 go-redis 客户端创建 Store。
func NewRedis(rdb redis.UniversalClient, opts ...RedisOption) (*RedisStore, error) {
	if rdb == nil {
		return nil, fmt.Errorf("llmsession: nil redis client")
	}
	s := &RedisStore{rdb: rdb, prefix: "beauty:llm:session:"}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

func (s *RedisStore) key(id string) string { return s.prefix + id }

// Load 实现 session.Store。
func (s *RedisStore) Load(ctx context.Context, id string) (*session.Session, error) {
	b, err := s.rdb.Get(ctx, s.key(id)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sess session.Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("llmsession: decode: %w", err)
	}
	sess.ID = id
	return &sess, nil
}

// Save 实现 session.Store。
func (s *RedisStore) Save(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.ID == "" {
		return fmt.Errorf("llmsession: invalid session")
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = time.Now().UTC()
	}
	b, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.key(sess.ID), b, s.ttl).Err()
}

var _ session.Store = (*RedisStore)(nil)
