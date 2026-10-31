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
//	sketch      — 概率数据结构(HyperLogLog、Count-Min、TDigest 分位数、蓄水池采样)
//	filter      — 近似集合过滤器(Bloom、Cuckoo,纯内存)
//	roaring     — Roaring 压缩位图(海量稀疏整数集,array/bitmap 自适应容器)
//	skiplist    — 泛型有序跳表映射(点查/范围/名次)
//	intervaltree— 区间树(treap + maxHigh 增广,区间相交/点覆盖查询)
//	actrie      — Aho-Corasick 多模匹配 + 双数组 Trie(敏感词/词典/分词)
//	simhash     — SimHash / MinHash 近似相似去重
//	diff        — LCS 差异 + 补丁、JSON Patch(RFC6902)、三路合并
//	crdt        — 无冲突复制数据类型(GCounter/PNCounter/GSet/ORSet/LWW)
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
