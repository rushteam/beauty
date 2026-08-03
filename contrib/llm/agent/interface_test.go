package agent_test

import "github.com/rushteam/beauty/contrib/llm/agent"

// 编译期断言:统一抽象的实现关系。
var (
	_ agent.StreamAgent = (*agent.Runner)(nil)
	_ agent.StreamAgent = (*agent.Chain)(nil)
	_ agent.StreamAgent = (*agent.Team)(nil)
	_ agent.StreamAgent = (*agent.Parallel)(nil)
	_ agent.Agent       = (*agent.BestOfN)(nil)
	_ agent.Agent       = (*agent.VerifyLoop)(nil)
)
