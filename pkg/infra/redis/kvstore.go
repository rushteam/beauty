package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/pkg/kvstore"
)

// incrScript 原子地 INCRBY,并且只在 key 此前不存在时才设置 TTL(已存在则不刷新
// ttl)——契合 kvstore.Store.Incr 的语义(计数窗口的过期时间从首次创建起算)。
var incrScript = redis.NewScript(`
local exists = redis.call("exists", KEYS[1])
local v = redis.call("incrby", KEYS[1], ARGV[1])
if exists == 0 and tonumber(ARGV[2]) > 0 then
	redis.call("pexpire", KEYS[1], ARGV[2])
end
return v`)

// Store 用 Redis 实现 pkg/kvstore.Store,给 counter/cooldown/idempotency 等原语一个
// 跨实例共享的后端(替代它们默认的单进程内存实现)。所有方法都是原子操作。
//
// 零值不可用,用 NewStore / NewStoreFromConfig 构造。
type Store struct {
	client redis.UniversalClient
	prefix string
}

// StoreOption 配置 Store。
type StoreOption func(*Store)

// WithStoreKeyPrefix 设置 key 前缀(默认无前缀,由各原语自行命名 key)。
func WithStoreKeyPrefix(prefix string) StoreOption {
	return func(s *Store) { s.prefix = prefix }
}

// NewStore 用已有 Redis 客户端创建 Store。client 由调用方管理生命周期。
func NewStore(client redis.UniversalClient, opts ...StoreOption) *Store {
	s := &Store{client: client}
	for _, o := range opts {
		o(s)
	}
	return s
}

// NewStoreFromConfig 复用 Config 建连接(并 Ping 校验)后创建 Store。
func NewStoreFromConfig(c *Config, opts ...StoreOption) (*Store, error) {
	client, err := pingClient(c)
	if err != nil {
		return nil, fmt.Errorf("redis kvstore: %w", err)
	}
	return NewStore(client, opts...), nil
}

func (s *Store) k(key string) string { return s.prefix + key }

// Incr 实现 kvstore.Store(INCRBY + 首次 PEXPIRE)。
func (s *Store) Incr(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	v, err := incrScript.Run(ctx, s.client, []string{s.k(key)}, delta, ttl.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis kvstore: incr %s: %w", key, err)
	}
	return v, nil
}

// GetInt 实现 kvstore.Store。不存在/已过期返回 (0, false, nil)。
func (s *Store) GetInt(ctx context.Context, key string) (int64, bool, error) {
	v, err := s.client.Get(ctx, s.k(key)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("redis kvstore: getint %s: %w", key, err)
	}
	return v, true, nil
}

// Get 实现 kvstore.Store。不存在/已过期返回 (nil, false, nil)。
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := s.client.Get(ctx, s.k(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("redis kvstore: get %s: %w", key, err)
	}
	return b, true, nil
}

// Set 实现 kvstore.Store(ttl<=0 表示不过期)。
func (s *Store) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if ttl < 0 {
		ttl = 0 // go-redis: 0 表示不设过期
	}
	if err := s.client.Set(ctx, s.k(key), val, ttl).Err(); err != nil {
		return fmt.Errorf("redis kvstore: set %s: %w", key, err)
	}
	return nil
}

// SetNX 实现 kvstore.Store(ttl<=0 表示不过期)。
func (s *Store) SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	if ttl < 0 {
		ttl = 0
	}
	ok, err := s.client.SetNX(ctx, s.k(key), val, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis kvstore: setnx %s: %w", key, err)
	}
	return ok, nil
}

// TTL 实现 kvstore.Store。不存在返回 (0, false, nil);永不过期返回一个很大的值 + true。
func (s *Store) TTL(ctx context.Context, key string) (time.Duration, bool, error) {
	d, err := s.client.PTTL(ctx, s.k(key)).Result()
	if err != nil {
		return 0, false, fmt.Errorf("redis kvstore: ttl %s: %w", key, err)
	}
	switch d {
	case -2: // key 不存在
		return 0, false, nil
	case -1: // 存在但无过期
		return time.Duration(1<<62 - 1), true, nil
	default:
		return d, true, nil
	}
}

// Delete 实现 kvstore.Store(不存在也不报错)。
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, s.k(key)).Err(); err != nil {
		return fmt.Errorf("redis kvstore: delete %s: %w", key, err)
	}
	return nil
}

var _ kvstore.Store = (*Store)(nil)
