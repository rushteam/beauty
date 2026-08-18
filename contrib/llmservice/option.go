package llmservice

import (
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/pkg/dlock"
	"github.com/rushteam/beauty/pkg/shard"
)

// ServiceOption 配置 AgentService。
type ServiceOption func(*AgentService)

// Workers 设置 worker 并发数(默认 2)。
func Workers(n int) ServiceOption {
	return func(s *AgentService) {
		if n > 0 {
			s.workers = n
		}
	}
}

// TaskBuffer 设置任务队列缓冲区大小(默认 64)。
func TaskBuffer(n int) ServiceOption {
	return func(s *AgentService) {
		if n > 0 {
			s.taskBuf = n
		}
	}
}

// WithStore 设置 RunStore(checkpoint 持久化)。nil 时 Agent 用内置 memory store。
func WithStore(store agent.RunStore) ServiceOption {
	return func(s *AgentService) {
		s.store = store
	}
}

// WithLocker 启用分布式锁:同一 run_id 的 Continue 只在一个 worker 上执行。
// 多节点部署时传入 etcd/redis locker 保证跨进程互斥。
func WithLocker(l dlock.Locker) ServiceOption {
	return func(s *AgentService) {
		s.locker = l
	}
}

// WithSharder 设置亲和路由:HTTP Handler 内部根据 session key 判断是否本机处理。
// 非本机的请求通过 shard.Router 反向代理到归属节点。
func WithSharder(sharder *shard.Sharder) ServiceOption {
	return func(s *AgentService) {
		s.sharder = sharder
	}
}
