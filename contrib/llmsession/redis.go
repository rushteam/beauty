package llmsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

func (s *RedisStore) key(id string) (string, error) {
	safe, err := sanitizeSessionID(id)
	if err != nil {
		return "", err
	}
	return s.prefix + safe, nil
}

func sanitizeSessionID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("llmsession: empty session id")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			return "", fmt.Errorf("llmsession: invalid session id %q", id)
		}
	}
	if strings.Contains(id, "..") {
		return "", fmt.Errorf("llmsession: invalid session id %q", id)
	}
	return id, nil
}

// Load 实现 session.Store。
func (s *RedisStore) Load(ctx context.Context, id string) (*session.Session, error) {
	key, err := s.key(id)
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
	var sess session.Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("llmsession: decode: %w", err)
	}
	sess.ID = id
	return &sess, nil
}

// Save 实现 session.Store。不修改传入的 sess 对象。
func (s *RedisStore) Save(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.ID == "" {
		return fmt.Errorf("llmsession: invalid session")
	}
	key, err := s.key(sess.ID)
	if err != nil {
		return err
	}
	cp := *sess
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = time.Now().UTC()
	}
	b, err := json.Marshal(&cp)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, key, b, s.ttl).Err()
}

var _ session.Store = (*RedisStore)(nil)
