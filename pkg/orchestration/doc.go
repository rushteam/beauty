// Package orchestration 提供任务编排与调度机制,解决"按时间/顺序/事务语义执行一组工作"的问题。
//
// 选包标准:
//   - 核心能力是编排多步骤/延时/定时任务
//   - 不涉及实时游戏逻辑(那属于 game/)
//   - 可依赖 foundation/、resilience/(如 backoff)、store/(如 dlock)
//
// 已有子包:
//
//	saga        — Saga 模式(补偿事务编排)
//	txn         — 本地事务辅助
//	worker      — 后台 Worker 池(依赖 store/dlock)
//	scheduler   — 异步任务调度器(Submit/Pause/Resume)
//	timerqueue  — 最小堆延时队列(beauty.Service 集成,适合大量倒计时)
//	delayqueue  — 精确 time.Timer 延时队列(一次性任务)
package orchestration
