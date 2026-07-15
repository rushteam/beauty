// Package redis 提供基于 Redis 的基建适配:分布式锁/选主(实现 pkg/dlock)与
// 带 TTL 的原子 KV 存储(实现 pkg/kvstore.Store,给 counter/cooldown/idempotency
// 等原语一个真实的跨实例后端)。薄封装 github.com/redis/go-redis,不重新发明算法。
//
// 锁语义是"单节点 Redis"级别:SET NX PX 抢占 + Lua CAS 释放/续期。这在单个 Redis
// (或主从,主挂了 failover 期间有极小的双持窗口)上正确、够用,但不是跨多个独立
// Redis master 的 Redlock——如实标注这一点,不假装提供 Redlock 的强度。需要更强
// 保证时用 pkg/infra/etcd 或 pkg/infra/consul 后端。传入 ClusterClient 亦不改变
// 单节点语义(锁 key 落在单个 slot 上)。
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config Redis 连接配置(单节点)。
type Config struct {
	Addr     string // host:port
	Password string
	DB       int
}

// NewClient 按 Config 建立一个 go-redis 客户端(懒连接,不校验可达性)。
// 分布式锁与 KV 存储共用这一处构造。client 生命周期由调用方管理(负责 Close)。
func NewClient(c *Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     c.Addr,
		Password: c.Password,
		DB:       c.DB,
	})
}

// pingClient 建连接并 Ping 验证可达,供各 *FromConfig 构造函数快速失败,
// 避免把不可达的配置留到首次实际操作才暴露。
func pingClient(c *Config) (*redis.Client, error) {
	client := NewClient(c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: ping %s: %w", c.Addr, err)
	}
	return client, nil
}
