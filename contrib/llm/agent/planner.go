package agent

import (
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// Planner 是 agent 循环的「规划接缝」:给 Runner 一个机会在首轮模型调用前注入规划指令,
// 并对每一轮模型响应做后处理。它是机制而非策略——具体规划风格(ReAct / plan-and-execute /
// 自定义标记)由实现决定,Runner 只在两处接缝调用它:
//
//   - BuildPlanningInstruction:进入循环前调用一次,返回要并入 req.System 的指令(空=不注入)。
//   - ProcessPlanningResponse:每轮 callModel 之后调用,可就地清洗/改写响应(如剥离思考标签、
//     在识别到终态标记时把 Content 收敛为干净答案)。返回处理后的响应(可原样返回)。
type Planner interface {
	BuildPlanningInstruction(req *llm.Request) string
	ProcessPlanningResponse(step int, resp *llm.Response) *llm.Response
}

// ReAct 各标记默认值(可在 ReActPlanner 上覆盖)。模型被要求按
// PLANNING → REASONING/ACTION 交替 → 以 FinalMarker 收尾。
const (
	reactPlanningTag  = "/*PLANNING*/"
	reactReasoningTag = "/*REASONING*/"
	reactActionTag    = "/*ACTION*/"
	reactFinalMarker  = "FINAL ANSWER:"
)

// defaultReActInstruction 是注入到 system 的 ReAct 规划指令。
const defaultReActInstruction = `你在解决问题时请按 ReAct 方式组织输出:
- 先用 ` + reactPlanningTag + ` 段给出高层计划(要点式,简明)。
- 之后每一步:用 ` + reactReasoningTag + ` 段写出推理,用 ` + reactActionTag + ` 段说明要采取的动作
  (若需调用工具,直接发起工具调用即可,不必把入参写进文本)。
- 掌握足够信息后,另起一行以 "` + reactFinalMarker + `" 开头给出面向用户的最终答复,该行之后只写答复本身。`

// ReActPlanner 是 Planner 的 ReAct 实现:注入 ReAct 格式指令,并在响应文本出现
// FinalMarker 时把 Content 收敛为其后的干净答复(同时剥离 PLANNING/REASONING/ACTION 标记),
// 便于把 agent 的终态文本直接返回给用户。纯字符串处理,零外部依赖。
//
// 各字段为空时用上面的默认标记;可覆盖以适配自定义提示词风格。
type ReActPlanner struct {
	Instruction   string // 覆盖注入的规划指令(空用默认)
	FinalMarker   string // 覆盖终态标记(空用 "FINAL ANSWER:")
	PlanningTag   string // 覆盖 PLANNING 标记
	ReasoningTag  string // 覆盖 REASONING 标记
	ActionTag     string // 覆盖 ACTION 标记
	KeepReasoning bool   // true 时不剥离思考标记,仅在有 FinalMarker 时截取终态段
}

var _ Planner = (*ReActPlanner)(nil)

func (p *ReActPlanner) instruction() string {
	if p.Instruction != "" {
		return p.Instruction
	}
	return defaultReActInstruction
}

func (p *ReActPlanner) finalMarker() string {
	if p.FinalMarker != "" {
		return p.FinalMarker
	}
	return reactFinalMarker
}

// BuildPlanningInstruction 返回 ReAct 规划指令,注入到 system。
func (p *ReActPlanner) BuildPlanningInstruction(*llm.Request) string {
	return p.instruction()
}

// ProcessPlanningResponse 在响应含 FinalMarker 时把 Content 收敛为其后的最终答复;
// 否则(除非 KeepReasoning)剥离 PLANNING/REASONING/ACTION 标记行,保留正文。
// 带 tool_calls 的响应(动作阶段)不动其结构,仅清洗文本。
func (p *ReActPlanner) ProcessPlanningResponse(_ int, resp *llm.Response) *llm.Response {
	if resp == nil || resp.Content == "" {
		return resp
	}
	marker := p.finalMarker()
	if idx := strings.Index(resp.Content, marker); idx >= 0 {
		resp.Content = strings.TrimSpace(resp.Content[idx+len(marker):])
		return resp
	}
	if p.KeepReasoning {
		return resp
	}
	resp.Content = p.stripTags(resp.Content)
	return resp
}

// stripTags 去掉包含 PLANNING/REASONING/ACTION 标记的整行(标记通常各自独占一行做小节标题),
// 并压掉由此产生的多余空行,保留其余正文。
func (p *ReActPlanner) stripTags(s string) string {
	tags := []string{
		orDefault(p.PlanningTag, reactPlanningTag),
		orDefault(p.ReasoningTag, reactReasoningTag),
		orDefault(p.ActionTag, reactActionTag),
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		drop := false
		for _, tag := range tags {
			if strings.HasPrefix(trimmed, tag) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, ln)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
