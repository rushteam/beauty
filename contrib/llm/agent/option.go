package agent

import "github.com/rushteam/beauty/contrib/llm"

// Option 是 agent 运行时的类型安全选项。每种选项是独立类型,通过 GetOption[T] 泛型查找。
// 与 Runner struct 字段互补:struct 字段定义 agent 的基础配置,Option 用于 per-run 覆盖。
type Option interface {
	agentOption()
}

// GetOption 从 options 中查找类型为 T 的选项。多个同类型时返回最后一个(last-wins)。
func GetOption[T Option](options []Option) (T, bool) {
	var zero T
	var found T
	ok := false
	for _, o := range options {
		if v, is := o.(T); is {
			found = v
			ok = true
		}
	}
	if ok {
		return found, true
	}
	return zero, false
}

// WithModel 覆盖本次运行的模型。
type WithModel string

func (WithModel) agentOption() {}

// WithSystem 覆盖/追加本次运行的 system prompt。
type WithSystem string

func (WithSystem) agentOption() {}

// WithMaxSteps 覆盖本次运行的最大步数。
type WithMaxSteps int

func (WithMaxSteps) agentOption() {}

// WithTools 在本次运行中追加额外工具。
type WithTools []Tool

func (WithTools) agentOption() {}

// WithTemperature 覆盖本次运行的温度。
type WithTemperature float64

func (WithTemperature) agentOption() {}

// WithResponseFormat 覆盖本次运行的输出格式。
type WithResponseFormat struct{ Format *llm.ResponseFormat }

func (WithResponseFormat) agentOption() {}

// applyOptions 把 per-run options 合并到 Request 和运行参数上。
func applyOptions(req *llm.Request, tools *[]Tool, maxSteps *int, opts []Option) {
	for _, o := range opts {
		switch v := o.(type) {
		case WithModel:
			req.Model = string(v)
		case WithSystem:
			s := string(v)
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += s
		case WithMaxSteps:
			*maxSteps = int(v)
		case WithTools:
			*tools = append(*tools, v...)
		case WithTemperature:
			req.Temperature = float64(v)
		case WithResponseFormat:
			req.ResponseFormat = v.Format
		}
	}
}
