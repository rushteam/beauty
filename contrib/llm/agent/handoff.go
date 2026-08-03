package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// handoffMarker 是成员在终态文本里请求移交的行前缀:HANDOFF: <成员名> <交给该成员的输入>。
const handoffMarker = "HANDOFF:"

// HandoffConfig 是多 agent 移交的 loop-safety 护栏参数。零值用默认。
type HandoffConfig struct {
	// MaxHandoffs 是一次 Team.Run 允许的最大移交次数(0 用默认 16)。超过即停机。
	MaxHandoffs int
	// Window 是重复性检测的滑动窗口大小(0 用默认 4)。
	Window int
	// MinUnique 是窗口内至少应出现的不同移交目标数(0 用默认 2)。
	// 当最近 Window 次移交的不同目标数 < MinUnique 时,判定为 A↔B 打转,停机。
	MinUnique int
}

func (c HandoffConfig) maxHandoffs() int {
	if c.MaxHandoffs > 0 {
		return c.MaxHandoffs
	}
	return 16
}

func (c HandoffConfig) window() int {
	if c.Window > 0 {
		return c.Window
	}
	return 4
}

func (c HandoffConfig) minUnique() int {
	if c.MinUnique > 0 {
		return c.MinUnique
	}
	return 2
}

// handoffTracker 记录移交历史并施加护栏:超过 MaxHandoffs 或滑动窗口内目标过于重复即报错。
type handoffTracker struct {
	cfg     HandoffConfig
	history []string
}

// record 记录一次移交到 target;若触发护栏则返回描述性 error(调用方据此停机)。
func (t *handoffTracker) record(target string) error {
	t.history = append(t.history, target)
	if len(t.history) > t.cfg.maxHandoffs() {
		return fmt.Errorf("agent: handoff guard: exceeded max handoffs (%d)", t.cfg.maxHandoffs())
	}
	w := t.cfg.window()
	if len(t.history) >= w {
		window := t.history[len(t.history)-w:]
		uniq := make(map[string]struct{}, w)
		for _, h := range window {
			uniq[h] = struct{}{}
		}
		if len(uniq) < t.cfg.minUnique() {
			return fmt.Errorf("agent: handoff guard: repetitive handoffs detected (last %d targets have only %d unique: %v)", w, len(uniq), window)
		}
	}
	return nil
}

// Team 让若干具名 Agent 通过在终态文本里写 "HANDOFF: <name> <input>" 把控制权移交给同伴,
// 形成一个「协调器 / swarm」式的多 agent 循环。每次移交都过 handoffTracker 护栏(MaxHandoffs +
// 滑动窗口重复检测),避免 A↔B 打转或无限委托。成员可为任意 Agent,Team 本身也实现 Agent,
// 因此可再被 Chain / AgentAsTool 嵌套。
//
// 机制而非策略:移交的判定完全由模型输出的 HANDOFF 标记驱动,Team 只负责解析、路由与护栏;
// prompt/模型/工具仍由各成员(通常是 *Runner)自带。
type Team struct {
	Name    string           // 可选,用于 Info()
	Members map[string]Agent // 成员名 → agent
	Entry   string           // 起始成员名(必须在 Members 中)
	Config  HandoffConfig    // 护栏参数
	Prompt  string           // 可选:注入各成员 system 的移交说明(空用默认,自动附可移交成员名单)
}

var _ Agent = (*Team)(nil)

// Run 从 Entry 起循环:成员跑完 → 解析 HANDOFF 标记 → 护栏 record → 路由到目标成员;
// 成员未请求移交即视为终态,返回其响应。护栏报错或目标非法时,带最后一次响应停机并返回该错误。
func (tm *Team) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if len(tm.Members) == 0 {
		return nil, fmt.Errorf("agent: team has no members")
	}
	current := tm.Entry
	if current == "" {
		return nil, fmt.Errorf("agent: team entry is empty")
	}
	tracker := &handoffTracker{cfg: tm.Config}
	prompt := tm.handoffPrompt()

	// 第一步用调用方 Request;后续步把移交输入作为唯一 user 消息(与 Chain 一致)。
	input := req
	first := true
	var last *llm.Response
	for {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		member, ok := tm.Members[current]
		if !ok {
			return last, fmt.Errorf("agent: team: unknown member %q", current)
		}

		stepReq := input // first 步为调用方 Request;后续步已是 {Model, [User:移交输入]}
		if prompt != "" {
			if stepReq.System != "" {
				stepReq.System += "\n\n"
			}
			stepReq.System += prompt
		}

		runCtx := ctx
		if !first {
			runCtx = WithTrigger(ctx, TriggerTransfer, current)
		}
		resp, err := member.Run(runCtx, stepReq)
		if err != nil {
			return resp, err
		}
		last = resp

		target, handoffInput, ok := parseHandoff(resp.Content)
		if !ok {
			return resp, nil // 无移交标记 → 终态
		}
		if _, exists := tm.Members[target]; !exists {
			return resp, fmt.Errorf("agent: team: member %q tried to hand off to unknown member %q", current, target)
		}
		if err := tracker.record(target); err != nil {
			return resp, err
		}
		if handoffInput == "" {
			// 未给出显式输入 → 沿用上一步输入内容,保证下一成员有上下文。
			handoffInput = firstUserContent(input)
		}
		current = target
		input = llm.Request{Model: req.Model, Messages: []llm.Message{{Role: llm.User, Content: handoffInput}}}
		first = false
	}
}

// Info 实现 Agent:汇总各成员暴露的工具声明。
func (tm *Team) Info() Info {
	var tools []llm.ToolDef
	for _, name := range tm.memberNames() {
		tools = append(tools, tm.Members[name].Info().Tools...)
	}
	return Info{Name: tm.Name, Description: "multi-agent team", Tools: tools}
}

// memberNames 返回排序后的成员名(稳定顺序,供 prompt 与 Info)。
func (tm *Team) memberNames() []string {
	names := make([]string, 0, len(tm.Members))
	for n := range tm.Members {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// handoffPrompt 返回注入成员 system 的移交说明。Prompt 非空则用它(并附成员名单);否则用默认。
func (tm *Team) handoffPrompt() string {
	names := strings.Join(tm.memberNames(), ", ")
	if tm.Prompt != "" {
		return tm.Prompt + "\n可移交的成员:" + names + "。"
	}
	return "你是一个多 agent 团队的成员。若需要把任务移交给更合适的同伴,请另起一行,以\n" +
		handoffMarker + " <成员名> <交给该成员的输入>\n结尾(成员名取自:" + names + ")。" +
		"若你能直接完成任务,请直接给出最终答复,不要写 " + handoffMarker + "。"
}

// parseHandoff 从文本中解析首个 HANDOFF 指令行,返回目标成员名与移交输入。
func parseHandoff(content string) (target, input string, ok bool) {
	for ln := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(ln)
		rest, found := strings.CutPrefix(trimmed, handoffMarker)
		if !found {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "", "", false
		}
		if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
			return rest[:sp], strings.TrimSpace(rest[sp+1:]), true
		}
		return rest, "", true
	}
	return "", "", false
}

// firstUserContent 返回 req 中首条消息的文本(用于移交时无显式输入的兜底)。
func firstUserContent(req llm.Request) string {
	if len(req.Messages) > 0 {
		return req.Messages[0].Content
	}
	return ""
}
