// Package foundation 是零或极少外部依赖的基础原语与数据结构,是整个框架的最底层。
//
// 选包标准:
//   - 不依赖框架内其他分组(resilience/store/messaging 等)
//   - 不依赖重量级第三方库(如需要,应放 contrib/)
//   - 解决的是通用编程问题,而非特定业务领域
//
// 已有子包:
//
//	ctxkey      — context key 辅助
//	safe        — panic recovery
//	generic     — 泛型工具函数
//	options     — functional options 模式
//	vars        — 字符串模板插值
//	buildinfo   — 构建信息注入
//	bitmap      — 位图
//	ringbuffer  — 环形缓冲区
//	chanx       — channel 扩展(如无界 chan)
//	fixedpoint  — 定点数运算
//	sketch      — 概率数据结构(HyperLogLog、Count-Min 等)
//	pagination  — 分页工具
//	priority    — 优先队列
//	pipeline    — 管道(stage→stage)
//	dag         — DAG 拓扑排序与并发执行
//	fsm         — 有限状态机
//	keyedmutex  — 按 key 细粒度互斥锁
//	syncx       — 并发工具集(singleflight、errgroup 扩展等)
//	xgo         — goroutine pool
//	semaphore   — 信号量
//	signals     — OS 信号处理
package foundation
