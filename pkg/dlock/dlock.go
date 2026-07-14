// Package dlock 定义跨进程分布式锁与选主(leader election)的后端无关接口。
//
// 背景:beauty 支持多实例部署,但很多场景要求"同一时刻只有一个实例做某件事"——
// 最典型是 Cron:多实例各自跑一遍会导致任务重复执行(发重奖励、重复扣款、
// 重复生成报表)。pkg/keyedmutex 是进程内锁,解决不了跨进程互斥;这里补上
// 跨进程版本的两个原语:
//
//   - Locker:分布式互斥锁,Lock/TryLock 拿到 Lock 后独占 key,Unlock 释放;
//   - Elector:持续选主,参选 key 下的 leader 身份,当选时收到回调,失去
//     leader(网络分区/进程崩溃/主动放弃)时回调的 ctx 被取消。
//
// 本包只定义接口 + 提供内存实现(单进程内多 goroutine 竞争,供开发/测试/
// 单实例部署使用)。真实跨进程后端见 pkg/infra/etcd(基于 etcd 官方
// client/v3/concurrency 包的 Session+Mutex / Session+Election,不重新发明
// 分布式锁算法)。遵循 beauty 纯标准库约定,核心接口零依赖。
package dlock

import "context"

// Lock 是一次成功获取的锁,持有期间独占对应 key。
type Lock interface {
	// Unlock 释放锁。幂等:重复调用不应返回错误或产生副作用。
	Unlock(ctx context.Context) error
}

// Locker 是跨进程分布式互斥锁。
type Locker interface {
	// Lock 阻塞直到获得 key 的锁,或 ctx 被取消(此时返回 ctx.Err())。
	Lock(ctx context.Context, key string) (Lock, error)

	// TryLock 非阻塞尝试获取 key 的锁。已被占用返回 (nil, false, nil)。
	TryLock(ctx context.Context, key string) (Lock, bool, error)
}

// Elector 是跨进程选主器:多个实例参选同一个 key,任意时刻至多一个当选 leader。
type Elector interface {
	// Run 参选 key 下的 leader 身份,阻塞运行直到 ctx 被取消(返回 ctx.Err())。
	//
	// 当选时,以 leaderCtx 调用 onElected——leaderCtx 在"失去 leader 身份"时被
	// cancel(包括:outer ctx 取消、租约/会话失效、主动放弃)。onElected 应把
	// leaderCtx 当作"仍是 leader"的凭证:长期工作应持续检查 leaderCtx.Done(),
	// 一旦触发就必须停止对应工作(此时可能已有新 leader 当选)。
	//
	// onElected 返回后(通常因 leaderCtx.Done()),若 outer ctx 仍存活,Run 会
	// 自动重新参选;直到 outer ctx 取消才真正退出并返回 ctx.Err()。
	Run(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error
}
