// Package game 提供游戏引擎机制,分为 core(实时模拟)和 meta(成长/社交)两个子组。
//
// 选包标准:
//   - 解决的问题在非游戏场景中极少出现(帧同步、AOI、战斗评分、战利品掉落等)
//   - 通用数据结构(如 FSM、优先队列)即使被游戏包使用,仍属于 foundation/
//   - 可依赖 foundation/、messaging/(如 stream)、orchestration/(如 delayqueue)
//
// Core 子包(实时模拟):
//
//	gameloop    — 定步长游戏循环(lockstep / 状态同步骨架)
//	match       — 有状态实时会话(actor 模型)
//	gameroom    — 房间 FSM(Waiting→Running→Draining)
//	spatial     — 网格空间索引(含 aoi 子包做 AOI 差集)
//	replicate   — 状态同步原语(DirtySet/Versions/Projector)
//	inputclock  — 输入时钟同步
//	snapbuf     — 快照环形缓冲
//	lagcomp     — 延迟补偿
//	versus      — 1v1 / NvN 对战编排
//	matchmaker  — 匹配队列
//	rating      — 评分系统(Glicko-2、TrueSkill 子包)
//	pathfind    — A* 寻路
//	geohash     — GeoHash 编码
//
// Meta 子包(成长/社交机制):
//
//	loot        — 战利品/掉落表
//	leveling    — 等级经验系统
//	questlog    — 任务/成就系统
//	leaderboard — 排行榜
//	reddot      — 红点提示系统
//	momentum    — 连续行为奖励/惩罚(连胜/连败)
package game
