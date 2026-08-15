package agent_test

import (
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// 编译期断言:统一抽象的实现关系。
var (
	_ agent.Agent = (*agent.Runner)(nil)
	_ agent.Agent = (*agent.Chain)(nil)
	_ agent.Agent = (*agent.Team)(nil)
	_ agent.Agent = (*agent.Parallel)(nil)
	_ agent.Agent = (*agent.BestOfN)(nil)
	_ agent.Agent = (*agent.VerifyLoop)(nil)
)
