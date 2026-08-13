package agent

import "context"

// ScopeByName 返回一个 ToolScope,只保留名称在 allowed 集合中的工具。
//
//	coderRunner := &agent.Runner{
//	    Tools: allTools,
//	    Scope: agent.ScopeByName("read_file", "write_file"),
//	}
func ScopeByName(allowed ...string) ToolScope {
	set := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		set[n] = struct{}{}
	}
	return ToolScopeFunc(func(_ context.Context, _ int, tools []Tool) []Tool {
		out := make([]Tool, 0, len(allowed))
		for _, t := range tools {
			if _, ok := set[t.Def.Name]; ok {
				out = append(out, t)
			}
		}
		return out
	})
}

// ScopeExclude 返回一个 ToolScope,排除名称在 excluded 集合中的工具。
func ScopeExclude(excluded ...string) ToolScope {
	set := make(map[string]struct{}, len(excluded))
	for _, n := range excluded {
		set[n] = struct{}{}
	}
	return ToolScopeFunc(func(_ context.Context, _ int, tools []Tool) []Tool {
		out := make([]Tool, 0, len(tools))
		for _, t := range tools {
			if _, ok := set[t.Def.Name]; !ok {
				out = append(out, t)
			}
		}
		return out
	})
}

// ScopeByStep 返回一个 ToolScope,按步数阶段切换工具可用性。
// phases 的 key 是步数阈值(含),value 是该阈值之后可用的工具名列表。
func ScopeByStep(phases map[int][]string) ToolScope {
	type phase struct {
		from  int
		names map[string]struct{}
	}
	sorted := make([]phase, 0, len(phases))
	for k, names := range phases {
		m := make(map[string]struct{}, len(names))
		for _, n := range names {
			m[n] = struct{}{}
		}
		sorted = append(sorted, phase{from: k, names: m})
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].from > sorted[i].from {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return ToolScopeFunc(func(_ context.Context, step int, tools []Tool) []Tool {
		var allowed map[string]struct{}
		for _, p := range sorted {
			if step >= p.from {
				allowed = p.names
				break
			}
		}
		if allowed == nil {
			return tools
		}
		out := make([]Tool, 0, len(allowed))
		for _, t := range tools {
			if _, ok := allowed[t.Def.Name]; ok {
				out = append(out, t)
			}
		}
		return out
	})
}

// ChainScopes 串联多个 ToolScope,依次应用。
func ChainScopes(scopes ...ToolScope) ToolScope {
	return ToolScopeFunc(func(ctx context.Context, step int, tools []Tool) []Tool {
		for _, s := range scopes {
			if s != nil {
				tools = s.Filter(ctx, step, tools)
			}
		}
		return tools
	})
}
