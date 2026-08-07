// Package prompt 是 contrib/llm 的 prompt 组装协议:把散落在各层(人设、技能目录、规划指令、
// 会话摘要、RAG 上下文、护栏…)的 prompt 片段统一声明为 Slot,由 Assembler 按 Position→Priority
// 规则组装成最终的 llm.Request。
//
// 两种集成模式(由 Assembler.FullControl 决定):
//
//   - 增量模式(默认):Assembler 在已有 req.System 之后追加 slot 内容。可与
//     Session.Manager、Runner.Planner 共存——各管各的。适合渐进式接入。
//
//   - 全权模式(FullControl=true):Assembler 完全接管 req.System 的构建,
//     忽略 Session/Planner 先前注入的内容。所有 prompt 片段都通过 Slot 声明,
//     Assembler 是唯一的 source of truth。使用时应将 Runner.Planner 置 nil,
//     Session 的摘要通过 ContentFunc slot 拉取而非由 Session.prepare() 注入。
//
// 典型用法(全权模式):
//
//	asm := prompt.New(
//	    prompt.SystemSlot("persona", 0, "你是…"),
//	    prompt.SystemSlot("skills", 50, "").Dynamic(func(prompt.Context) string { return sk.SystemPrompt() }),
//	    prompt.AfterSlot("guardrail", llm.System, 0, "不要…"),
//	)
//	asm.FullControl = true
//	runner := &agent.Runner{
//	    Hooks: agent.Hooks{BeforeModel: asm.Hook()},
//	}
//
// Token 预算:
//
//   - Slot.MaxTokens:单 slot 内容上限,超出时截断(保护单个 RAG 块不吞掉整个窗口)。
//   - Assembler.SystemBudget:所有 System slot 合计上限,超出时从低优先级(Priority 数值大)开始整条丢弃。
//   - Assembler.TokenCounter:自定义计数器(nil 时用 utf8.RuneCountInString 作粗略近似)。
//
// 边界(机制而非策略):Assembler 只负责"按声明组装+预算裁剪",不关心内容本身;
// prompt 策略、内容审查都是 policy,由使用方在 Slot 层面或外部决定。
package prompt

import (
	"context"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm"
)

// Position 决定 Slot 内容在最终 prompt 中的放置位置。
type Position int

const (
	// System 把内容合并进 Request.System。
	// 多个 System slot 按 Priority 排序后以 "\n\n" 拼接。
	// FullControl=false(默认)时追加在原 Request.System 之后;
	// FullControl=true 时完全替换 Request.System。
	System Position = iota
	// Before 作为消息插入到 Request.Messages 最前面(在历史之前)。
	Before
	// After 作为消息插入到最后一条 user 消息之前(在历史之后、当前输入之前)。
	// 若无 user 消息,则追加到末尾。典型用途:RAG 检索结果、上下文注入。
	After
	// Chat 按 Depth 从消息列表末尾往上插入。Depth=0 追加到末尾,Depth=1 在倒数第一条之前。
	// 同 Depth 的 slot 按 Priority 升序排列(值小在前)。典型用途:实时插话、微调指令。
	Chat
)

// String 返回 Position 的可读名。
func (p Position) String() string {
	switch p {
	case System:
		return "system"
	case Before:
		return "before"
	case After:
		return "after"
	case Chat:
		return "chat"
	default:
		return "unknown"
	}
}

// Slot 是 prompt 中的一个"插槽"——带有位置、优先级和角色属性的 prompt 片段。
// 多个 Slot 由 Assembler.Build 按规则组装成最终的 llm.Request。
//
// Position=System 时 Role 字段无意义(始终作为 system 内容);
// 其余 Position 必须设置 Role 以决定插入消息的角色。
type Slot struct {
	ID       string
	Role     llm.Role // Position=System 时忽略
	Position Position
	Depth    int // 仅 Position=Chat 时有效
	Priority int // 同 Position 内排序:值越小越靠前
	Content  string
	Enabled  bool

	// MaxTokens 限制单 slot 内容的 token 上限(0=不限)。
	// 超出时按 Assembler.TokenCounter 截断,保护单个 slot 不吞掉整个窗口。
	MaxTokens int

	// ContentFunc 提供动态内容;非 nil 时忽略 Content。
	// 在每次 Build 时调用,适合 RAG 结果、实时摘要等随请求变化的内容。
	ContentFunc func(ctx Context) string

	// Condition 动态启用条件;nil 视为始终启用。
	// 例:仅首轮注入规划指令。
	Condition func(ctx Context) bool

	// Source 标识来源,便于 Snapshot 调试("planner"、"skills"、"session"…)。
	Source string
}

// Dynamic 设置动态内容函数并返回 Slot 自身(值拷贝),便于链式构造:
//
//	prompt.SystemSlot("skills", 50, "").Dynamic(func(ctx prompt.Context) string { return sk.SystemPrompt() })
func (s Slot) Dynamic(fn func(Context) string) Slot {
	s.ContentFunc = fn
	return s
}

// When 设置启用条件并返回 Slot 自身(值拷贝),便于链式构造:
//
//	prompt.SystemSlot("planner", 100, reactInstr).When(func(ctx prompt.Context) bool { return ctx.Step == 1 })
func (s Slot) When(fn func(Context) bool) Slot {
	s.Condition = fn
	return s
}

// WithMaxTokens 设置 token 上限并返回 Slot 自身(值拷贝)。
func (s Slot) WithMaxTokens(n int) Slot {
	s.MaxTokens = n
	return s
}

// WithSource 设置来源标识并返回 Slot 自身(值拷贝)。
func (s Slot) WithSource(src string) Slot {
	s.Source = src
	return s
}

// ---- 便捷构造函数 ----
// 均默认 Enabled=true。可选属性通过链式方法设置。

// SystemSlot 创建 Position=System 的 slot(Role 无意义,自动忽略)。
func SystemSlot(id string, priority int, content string) Slot {
	return Slot{ID: id, Position: System, Priority: priority, Content: content, Enabled: true}
}

// BeforeSlot 创建 Position=Before 的 slot。
func BeforeSlot(id string, role llm.Role, priority int, content string) Slot {
	return Slot{ID: id, Role: role, Position: Before, Priority: priority, Content: content, Enabled: true}
}

// AfterSlot 创建 Position=After 的 slot。
func AfterSlot(id string, role llm.Role, priority int, content string) Slot {
	return Slot{ID: id, Role: role, Position: After, Priority: priority, Content: content, Enabled: true}
}

// ChatSlot 创建 Position=Chat 的 slot。
func ChatSlot(id string, role llm.Role, depth, priority int, content string) Slot {
	return Slot{ID: id, Role: role, Position: Chat, Depth: depth, Priority: priority, Content: content, Enabled: true}
}

// Context 是 Build 时传给 ContentFunc 和 Condition 的上下文信息。
type Context struct {
	Step         int
	MessageCount int
	Messages     []llm.Message
}

// Assembler 收集 Slot 并组装最终 prompt。并发安全。
type Assembler struct {
	// FullControl 决定 System slot 如何处理 req.System:
	//   - false(默认):在原 req.System 之后追加。与已有 Session/Planner 共存。
	//   - true:完全替换 req.System。Assembler 是 system prompt 的唯一来源。
	FullControl bool

	// SystemBudget 是所有 System slot 合计的 token 上限(0=不限)。
	// 超出时从低优先级(Priority 值大)开始整条丢弃,直到总量降到预算内。
	SystemBudget int

	// TokenCounter 计算字符串的 token 数;nil 时用 utf8.RuneCountInString(粗略近似)。
	// 生产环境可接 tiktoken 等精确计数器。
	TokenCounter func(string) int

	mu    sync.RWMutex
	slots []Slot
}

// New 创建 Assembler 并注册初始 slot。
func New(slots ...Slot) *Assembler {
	a := &Assembler{slots: make([]Slot, len(slots))}
	copy(a.slots, slots)
	return a
}

// Add 注册一个或多个 Slot。
func (a *Assembler) Add(slots ...Slot) {
	a.mu.Lock()
	a.slots = append(a.slots, slots...)
	a.mu.Unlock()
}

// Remove 按 ID 移除所有匹配的 Slot。
func (a *Assembler) Remove(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, s := range a.slots {
		if s.ID != id {
			a.slots[n] = s
			n++
		}
	}
	a.slots = a.slots[:n]
}

// SetEnabled 按 ID 设置所有匹配 Slot 的启用状态。
func (a *Assembler) SetEnabled(id string, enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.slots {
		if a.slots[i].ID == id {
			a.slots[i].Enabled = enabled
		}
	}
}

// SetContent 按 ID 更新所有匹配 Slot 的静态内容。
func (a *Assembler) SetContent(id string, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.slots {
		if a.slots[i].ID == id {
			a.slots[i].Content = content
		}
	}
}

func (a *Assembler) countTokens(s string) int {
	if a.TokenCounter != nil {
		return a.TokenCounter(s)
	}
	return utf8.RuneCountInString(s)
}

// Build 把所有启用的 Slot 按 Position→Priority 组装到 req 中,返回组装后的新请求。
// 不修改传入的 req(消息切片会被拷贝)。
func (a *Assembler) Build(ctx Context, req llm.Request) llm.Request {
	a.mu.RLock()
	raw := make([]Slot, len(a.slots))
	copy(raw, a.slots)
	fullCtrl := a.FullControl
	budget := a.SystemBudget
	a.mu.RUnlock()

	active := a.resolve(raw, ctx)
	if len(active) == 0 {
		if fullCtrl {
			out := req
			out.System = ""
			return out
		}
		return req
	}

	var (
		systemParts []string
		beforeMsgs  []llm.Message
		afterMsgs   []llm.Message
		chatSlots   []chatEntry
	)
	for _, rs := range active {
		switch rs.Position {
		case System:
			systemParts = append(systemParts, rs.content)
		case Before:
			beforeMsgs = append(beforeMsgs, llm.Message{Role: rs.Role, Content: rs.content})
		case After:
			afterMsgs = append(afterMsgs, llm.Message{Role: rs.Role, Content: rs.content})
		case Chat:
			chatSlots = append(chatSlots, chatEntry{
				depth:    rs.Depth,
				priority: rs.Priority,
				msg:      llm.Message{Role: rs.Role, Content: rs.content},
			})
		}
	}

	// System 预算裁剪:从低优先级(尾部)开始整条丢弃。
	if budget > 0 && len(systemParts) > 0 {
		systemParts = a.applyBudget(systemParts, budget)
	}

	out := req
	if fullCtrl {
		out.System = strings.Join(systemParts, "\n\n")
	} else {
		out.System = joinSystem(req.System, systemParts)
	}
	out.Messages = buildMessages(req.Messages, beforeMsgs, afterMsgs, chatSlots)
	return out
}

// applyBudget 对已按 Priority 升序排列的 parts,从尾部(低优先级)开始丢弃,
// 直到总 token 数 ≤ budget。分隔符 "\n\n" 的开销也计入。
func (a *Assembler) applyBudget(parts []string, budget int) []string {
	const sep = "\n\n"
	sepCost := a.countTokens(sep)

	total := 0
	for i, p := range parts {
		total += a.countTokens(p)
		if i > 0 {
			total += sepCost
		}
	}
	for total > budget && len(parts) > 0 {
		last := parts[len(parts)-1]
		total -= a.countTokens(last)
		if len(parts) > 1 {
			total -= sepCost
		}
		parts = parts[:len(parts)-1]
	}
	return parts
}

// Hook 返回可直接用于 agent.Hooks.BeforeModel 的函数。
// 每次调模型前,Assembler 根据当前 step 和消息重新组装 prompt。
func (a *Assembler) Hook() func(ctx context.Context, step int, req *llm.Request) error {
	return func(_ context.Context, step int, req *llm.Request) error {
		actx := Context{
			Step:         step,
			MessageCount: len(req.Messages),
			Messages:     req.Messages,
		}
		*req = a.Build(actx, *req)
		return nil
	}
}

// ChainHooks 把多个 BeforeModel 签名的 hook 串联:按顺序执行,任一返回 error 则中断。
// 用于在 Assembler.Hook() 之外还有自定义 hook(如日志、限流)的场景。
//
//	runner.Hooks.BeforeModel = prompt.ChainHooks(myLogger, asm.Hook(), myGuard)
func ChainHooks(hooks ...func(context.Context, int, *llm.Request) error) func(context.Context, int, *llm.Request) error {
	return func(ctx context.Context, step int, req *llm.Request) error {
		for _, h := range hooks {
			if h == nil {
				continue
			}
			if err := h(ctx, step, req); err != nil {
				return err
			}
		}
		return nil
	}
}

// ResolvedSlot 是 Snapshot 的输出:一个已解析(过滤+求值+截断)的 slot,便于调试和日志。
type ResolvedSlot struct {
	ID       string
	Source   string
	Position Position
	Priority int
	Role     llm.Role
	Depth    int
	Content  string
}

// Snapshot 返回当前所有启用 slot 的解析结果(不修改任何状态),用于调试/日志。
func (a *Assembler) Snapshot(ctx Context) []ResolvedSlot {
	a.mu.RLock()
	raw := make([]Slot, len(a.slots))
	copy(raw, a.slots)
	a.mu.RUnlock()

	active := a.resolve(raw, ctx)
	out := make([]ResolvedSlot, len(active))
	for i, rs := range active {
		out[i] = ResolvedSlot{
			ID:       rs.ID,
			Source:   rs.Source,
			Position: rs.Position,
			Priority: rs.Priority,
			Role:     rs.Role,
			Depth:    rs.Depth,
			Content:  rs.content,
		}
	}
	return out
}

// ---- internal ----

type resolved struct {
	Slot
	content string
}

type chatEntry struct {
	depth    int
	priority int
	msg      llm.Message
}

// resolve 过滤 + 求值 + MaxTokens 截断 + 排序。
func (a *Assembler) resolve(slots []Slot, ctx Context) []resolved {
	var out []resolved
	for _, s := range slots {
		if !s.Enabled {
			continue
		}
		if s.Condition != nil && !s.Condition(ctx) {
			continue
		}
		content := s.Content
		if s.ContentFunc != nil {
			content = s.ContentFunc(ctx)
		}
		if content == "" {
			continue
		}
		if s.MaxTokens > 0 {
			content = a.truncate(content, s.MaxTokens)
		}
		out = append(out, resolved{Slot: s, content: content})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].Priority < out[j].Priority
	})
	return out
}

// truncate 将 s 截断到不超过 maxTokens 个 token。使用二分查找适配任意 TokenCounter。
func (a *Assembler) truncate(s string, maxTokens int) string {
	if a.countTokens(s) <= maxTokens {
		return s
	}
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if a.countTokens(string(runes[:mid])) <= maxTokens {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return ""
	}
	return string(runes[:lo])
}

func joinSystem(base string, parts []string) string {
	all := make([]string, 0, 1+len(parts))
	if base != "" {
		all = append(all, base)
	}
	all = append(all, parts...)
	return strings.Join(all, "\n\n")
}

func buildMessages(orig, before, after []llm.Message, chat []chatEntry) []llm.Message {
	n := len(before) + len(orig) + len(after) + len(chat)
	msgs := make([]llm.Message, 0, n)
	msgs = append(msgs, before...)
	msgs = append(msgs, orig...)

	if len(after) > 0 {
		msgs = insertBeforeLastUser(msgs, after)
	}

	if len(chat) > 0 {
		// depth DESC → priority ASC:深处先插,同深度低优先级先插,保持最终正序。
		sort.SliceStable(chat, func(i, j int) bool {
			if chat[i].depth != chat[j].depth {
				return chat[i].depth > chat[j].depth
			}
			return chat[i].priority < chat[j].priority
		})
		for _, ce := range chat {
			msgs = insertAtDepth(msgs, ce.depth, ce.msg)
		}
	}
	return msgs
}

// insertBeforeLastUser 在最后一条 Role=User 消息之前插入 insert;无 user 消息则追加到末尾。
func insertBeforeLastUser(msgs []llm.Message, insert []llm.Message) []llm.Message {
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.User {
			idx = i
			break
		}
	}
	if idx < 0 {
		return append(msgs, insert...)
	}
	result := make([]llm.Message, 0, len(msgs)+len(insert))
	result = append(result, msgs[:idx]...)
	result = append(result, insert...)
	result = append(result, msgs[idx:]...)
	return result
}

// insertAtDepth 在 msgs[len-depth] 处插入一条消息。
func insertAtDepth(msgs []llm.Message, depth int, msg llm.Message) []llm.Message {
	idx := len(msgs) - depth
	if idx < 0 {
		idx = 0
	}
	if idx > len(msgs) {
		idx = len(msgs)
	}
	result := make([]llm.Message, 0, len(msgs)+1)
	result = append(result, msgs[:idx]...)
	result = append(result, msg)
	result = append(result, msgs[idx:]...)
	return result
}
