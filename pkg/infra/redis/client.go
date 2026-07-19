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
	"log/slog"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

// Config Redis 连接配置(单节点)。连接池/超时字段为可选,零值用 go-redis 默认。
type Config struct {
	Addr     string // host:port
	Password string
	DB       int

	PoolSize     int           // 连接池大小(0 用默认:10*GOMAXPROCS)
	MinIdleConns int           // 最小空闲连接
	DialTimeout  time.Duration // 建连超时
	ReadTimeout  time.Duration // 读超时
	WriteTimeout time.Duration // 写超时
}

// Option 配置 NewClient(如接入 OTel 埋点)。
type Option func(*clientConfig)

type clientConfig struct {
	tracing bool
	metrics bool
}

// WithTracing 给客户端接入 OTel 链路追踪(redisotel):每条 redis 命令产生 span,
// 用 beauty telemetry 配好的全局 TracerProvider(未配则 no-op)。
func WithTracing() Option { return func(c *clientConfig) { c.tracing = true } }

// WithMetrics 给客户端接入 OTel 命令级 metrics(redisotel):命令耗时、连接池状态等,
// 用全局 MeterProvider(未配则 no-op)。
func WithMetrics() Option { return func(c *clientConfig) { c.metrics = true } }

// NewClient 按 Config 建立一个 go-redis 客户端(懒连接,不校验可达性)。
// 分布式锁与 KV 存储共用这一处构造。client 生命周期由调用方管理(负责 Close)。
// 传 WithTracing()/WithMetrics() 即接入 OTel 可观测。
func NewClient(c *Config, opts ...Option) *redis.Client {
	var cfg clientConfig
	for _, o := range opts {
		o(&cfg)
	}
	client := redis.NewClient(&redis.Options{
		Addr:         c.Addr,
		Password:     c.Password,
		DB:           c.DB,
		PoolSize:     c.PoolSize,
		MinIdleConns: c.MinIdleConns,
		DialTimeout:  c.DialTimeout,
		ReadTimeout:  c.ReadTimeout,
		WriteTimeout: c.WriteTimeout,
	})
	if cfg.tracing {
		if err := redisotel.InstrumentTracing(client); err != nil {
			slog.Warn("redis: instrument tracing failed", "err", err)
		}
	}
	if cfg.metrics {
		if err := redisotel.InstrumentMetrics(client); err != nil {
			slog.Warn("redis: instrument metrics failed", "err", err)
		}
	}
	return client
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
