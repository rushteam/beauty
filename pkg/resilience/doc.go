// Package resilience 提供容错与流控机制,保护服务在异常条件下仍可降级运行。
//
// 选包标准:
//   - 解决的是"调用可能失败/过载"这类弹性问题
//   - 可依赖 foundation/ 和 store/(如 kvstore),不可反向依赖上层
//
// 已有子包:
//
//	backoff         — 指数退避 / 自定义退避策略
//	circuitbreaker  — 熔断器
//	ratelimit       — 令牌桶 / 滑动窗口限流
//	hedge           — 对冲请求(backup request)
//	timeout         — 超时控制
//	throttle        — 节流(与 ratelimit 互补,侧重调用频率平滑)
//	cooldown        — 冷却计时(依赖 store/kvstore)
//	counter         — 分布式计数器(依赖 store/kvstore)
package resilience
