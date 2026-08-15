package llm

// MessageFilter 是对单条消息的谓词。返回 true 表示保留该消息。
type MessageFilter func(Message) bool

// Apply 过滤消息切片,仅保留通过 filter 的消息。
func (f MessageFilter) Apply(msgs []Message) []Message {
	if f == nil {
		return nil
	}
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if f(m) {
			out = append(out, m)
		}
	}
	return out
}

// And 返回一个 filter:仅当所有子 filter 均通过时才保留消息。
func And(filters ...MessageFilter) MessageFilter {
	return func(m Message) bool {
		for _, f := range filters {
			if f != nil && !f(m) {
				return false
			}
		}
		return true
	}
}

// Or 返回一个 filter:任一子 filter 通过即保留消息。
func Or(filters ...MessageFilter) MessageFilter {
	return func(m Message) bool {
		for _, f := range filters {
			if f != nil && f(m) {
				return true
			}
		}
		return false
	}
}

// Not 返回给定 filter 的逻辑取反。
func Not(f MessageFilter) MessageFilter {
	return func(m Message) bool {
		if f == nil {
			return true
		}
		return !f(m)
	}
}

// BySource 保留来自指定来源的消息。
func BySource(sources ...SourceType) MessageFilter {
	set := make(map[SourceType]struct{}, len(sources))
	for _, s := range sources {
		set[s] = struct{}{}
	}
	return func(m Message) bool {
		_, ok := set[m.Source]
		return ok
	}
}

// ByRole 保留指定角色的消息。
func ByRole(roles ...Role) MessageFilter {
	set := make(map[Role]struct{}, len(roles))
	for _, r := range roles {
		set[r] = struct{}{}
	}
	return func(m Message) bool {
		_, ok := set[m.Role]
		return ok
	}
}

// ExcludeSources 排除来自指定来源的消息。
func ExcludeSources(sources ...SourceType) MessageFilter {
	set := make(map[SourceType]struct{}, len(sources))
	for _, s := range sources {
		set[s] = struct{}{}
	}
	return func(m Message) bool {
		_, ok := set[m.Source]
		return !ok
	}
}

// HasContent 保留 Content 非空的消息。
func HasContent() MessageFilter {
	return func(m Message) bool {
		return m.Content != ""
	}
}

// HasToolCalls 保留含 ToolCalls 的消息。
func HasToolCalls() MessageFilter {
	return func(m Message) bool {
		return len(m.ToolCalls) > 0
	}
}

// Persistable 返回 session 持久化常用 filter:
// 保留用户输入与模型产出,排除 history/context/middleware 注入
// (下次运行时会重新注入,无需重复存储)。
func Persistable() MessageFilter {
	return ExcludeSources(SourceHistory, SourceContext, SourceMiddleware)
}
