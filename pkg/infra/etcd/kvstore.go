package etcd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/rushteam/beauty/pkg/kvstore"
)

// Store 用 etcd 实现 pkg/kvstore.Store,给 counter/cooldown/idempotency 等原语一个
// 跨实例共享的后端。TTL 用 etcd lease 实现(可精确查询剩余时间),SetNX/Incr 用
// 事务/CAS 保证原子。不重新发明,只是薄封装 client/v3。
//
// 注意:etcd lease 的粒度是「秒」,ttl>0 但不足 1s 会被抬到 1s(cooldown 等需要
// 亚秒级精度时请用 pkg/infra/redis.Store,其基于 PEXPIRE 支持毫秒)。
//
// 零值不可用,用 NewStore / NewStoreFromConfig 构造。
type Store struct {
	client *clientv3.Client
	prefix string
}

// StoreOption 配置 Store。
type StoreOption func(*Store)

// WithStoreKeyPrefix 设置 key 前缀(默认无前缀,由各原语自行命名 key)。
func WithStoreKeyPrefix(prefix string) StoreOption {
	return func(s *Store) { s.prefix = prefix }
}

// NewStore 用已有 etcd 客户端创建 Store。client 由调用方管理生命周期。
func NewStore(client *clientv3.Client, opts ...StoreOption) *Store {
	s := &Store{client: client}
	for _, o := range opts {
		o(s)
	}
	return s
}

// NewStoreFromConfig 复用 Config 建连接后创建 Store,与配置中心/分布式锁共享连接约定。
func NewStoreFromConfig(c *Config, opts ...StoreOption) (*Store, error) {
	client, err := NewClient(c)
	if err != nil {
		return nil, fmt.Errorf("etcd kvstore: %w", err)
	}
	return NewStore(client, opts...), nil
}

func (s *Store) k(key string) string { return s.prefix + key }

// leaseSeconds 把 ttl 换算成 etcd lease 的秒数(ttl<=0 返回 0 表示不设租约;
// 不足 1s 抬到 1s——etcd lease 最小粒度)。
func leaseSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	if sec := int64(ttl / time.Second); sec >= 1 {
		return sec
	}
	return 1
}

// grant 申请一个 ttl 秒的 lease;sec<=0 返回 0(clientv3.NoLease)。
func (s *Store) grant(ctx context.Context, sec int64) (clientv3.LeaseID, error) {
	if sec <= 0 {
		return clientv3.NoLease, nil
	}
	resp, err := s.client.Grant(ctx, sec)
	if err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// Incr 实现 kvstore.Store:CAS 读-改-写循环。key 不存在时创建并绑定 ttl 的 lease;
// 已存在则用 WithIgnoreLease 只改值、保留原 lease(不刷新过期时间,契合计数窗口
// 从首次创建起算的语义)。
func (s *Store) Incr(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	fullKey := s.k(key)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		resp, err := s.client.Get(ctx, fullKey)
		if err != nil {
			return 0, fmt.Errorf("etcd kvstore: incr get %s: %w", key, err)
		}
		if len(resp.Kvs) == 0 {
			// 首次创建:带 lease 写入,用 CreateRevision==0 保证并发下只有一个成功。
			leaseID, err := s.grant(ctx, leaseSeconds(ttl))
			if err != nil {
				return 0, fmt.Errorf("etcd kvstore: incr grant %s: %w", key, err)
			}
			put := clientv3.OpPut(fullKey, strconv.FormatInt(delta, 10), clientv3.WithLease(leaseID))
			txn, err := s.client.Txn(ctx).
				If(clientv3.Compare(clientv3.CreateRevision(fullKey), "=", 0)).
				Then(put).Commit()
			if err != nil {
				return 0, fmt.Errorf("etcd kvstore: incr create %s: %w", key, err)
			}
			if txn.Succeeded {
				return delta, nil
			}
			continue // 并发已创建,重试走已存在分支
		}
		cur, err := strconv.ParseInt(string(resp.Kvs[0].Value), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("etcd kvstore: incr parse %s: %w", key, err)
		}
		next := cur + delta
		// 保留原 lease,只在 ModRevision 未变时写入(CAS)。
		put := clientv3.OpPut(fullKey, strconv.FormatInt(next, 10), clientv3.WithIgnoreLease())
		txn, err := s.client.Txn(ctx).
			If(clientv3.Compare(clientv3.ModRevision(fullKey), "=", resp.Kvs[0].ModRevision)).
			Then(put).Commit()
		if err != nil {
			return 0, fmt.Errorf("etcd kvstore: incr update %s: %w", key, err)
		}
		if txn.Succeeded {
			return next, nil
		}
		// 有并发修改,重试。
	}
}

// GetInt 实现 kvstore.Store。不存在/已过期返回 (0, false, nil)。
func (s *Store) GetInt(ctx context.Context, key string) (int64, bool, error) {
	b, ok, err := s.Get(ctx, key)
	if err != nil || !ok {
		return 0, ok, err
	}
	v, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("etcd kvstore: getint parse %s: %w", key, err)
	}
	return v, true, nil
}

// Get 实现 kvstore.Store。不存在/已过期返回 (nil, false, nil)。
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	resp, err := s.client.Get(ctx, s.k(key))
	if err != nil {
		return nil, false, fmt.Errorf("etcd kvstore: get %s: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, false, nil
	}
	return resp.Kvs[0].Value, true, nil
}

// Set 实现 kvstore.Store(ttl<=0 表示不过期)。
func (s *Store) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	leaseID, err := s.grant(ctx, leaseSeconds(ttl))
	if err != nil {
		return fmt.Errorf("etcd kvstore: set grant %s: %w", key, err)
	}
	var opts []clientv3.OpOption
	if leaseID != clientv3.NoLease {
		opts = append(opts, clientv3.WithLease(leaseID))
	}
	if _, err := s.client.Put(ctx, s.k(key), string(val), opts...); err != nil {
		return fmt.Errorf("etcd kvstore: set %s: %w", key, err)
	}
	return nil
}

// SetNX 实现 kvstore.Store:事务 If(CreateRevision==0) 才写入,返回是否写入成功。
func (s *Store) SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	fullKey := s.k(key)
	leaseID, err := s.grant(ctx, leaseSeconds(ttl))
	if err != nil {
		return false, fmt.Errorf("etcd kvstore: setnx grant %s: %w", key, err)
	}
	var putOpts []clientv3.OpOption
	if leaseID != clientv3.NoLease {
		putOpts = append(putOpts, clientv3.WithLease(leaseID))
	}
	txn, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(fullKey), "=", 0)).
		Then(clientv3.OpPut(fullKey, string(val), putOpts...)).Commit()
	if err != nil {
		return false, fmt.Errorf("etcd kvstore: setnx %s: %w", key, err)
	}
	if !txn.Succeeded && leaseID != clientv3.NoLease {
		// 没抢到,回收刚申请的 lease,避免泄漏。
		_, _ = s.client.Revoke(ctx, leaseID)
	}
	return txn.Succeeded, nil
}

// TTL 实现 kvstore.Store。不存在返回 (0, false, nil);无 lease(永不过期)返回一个
// 很大的值 + true;否则返回 lease 的剩余存活时间。
func (s *Store) TTL(ctx context.Context, key string) (time.Duration, bool, error) {
	resp, err := s.client.Get(ctx, s.k(key))
	if err != nil {
		return 0, false, fmt.Errorf("etcd kvstore: ttl get %s: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return 0, false, nil
	}
	leaseID := resp.Kvs[0].Lease
	if leaseID == 0 {
		return time.Duration(1<<62 - 1), true, nil // 无 lease = 永不过期
	}
	lresp, err := s.client.TimeToLive(ctx, clientv3.LeaseID(leaseID))
	if err != nil {
		return 0, false, fmt.Errorf("etcd kvstore: ttl lease %s: %w", key, err)
	}
	if lresp.TTL <= 0 {
		return 0, false, nil // lease 已过期(key 即将/已被回收)
	}
	return time.Duration(lresp.TTL) * time.Second, true, nil
}

// Delete 实现 kvstore.Store(不存在也不报错)。
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.client.Delete(ctx, s.k(key)); err != nil {
		return fmt.Errorf("etcd kvstore: delete %s: %w", key, err)
	}
	return nil
}

var _ kvstore.Store = (*Store)(nil)
