// Package store 提供存储与共享状态的抽象,是需要"记住东西"的机制的统一归属地。
//
// 选包标准:
//   - 核心能力是读/写/同步某种状态(内存、KV、锁、幂等记录等)
//   - 不涉及消息投递语义(那属于 messaging/)
//   - 可依赖 foundation/,不可依赖 resilience/messaging/transport/ 等上层
//
// 已有子包:
//
//	kvstore      — 通用 KV 存储接口(内存/Redis 后端)
//	cache        — 带 TTL 的本地缓存
//	dlock        — 分布式锁接口
//	ephemeral    — 临时数据(自动过期)
//	idempotency  — 幂等性保障(依赖 kvstore)
//	tally        — 计分/投票
//	shard        — 分片策略
//	loadbalance  — 负载均衡算法(P2C、WRR 等)
package store
