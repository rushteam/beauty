package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/rushteam/beauty/contrib/llm"
)

// RunStatus 是一次 Run/Continue 的结果状态(亦用于 Registry)。
type RunStatus string

const (
	StatusRunning   RunStatus = "running"
	StatusPaused    RunStatus = "paused"
	StatusDone      RunStatus = "done"
	StatusCancelled RunStatus = "cancelled"
	StatusError     RunStatus = "error"
)

// ErrMaxSteps 表示循环到达 MaxSteps 仍未得到终态回复。
var ErrMaxSteps = errors.New("agent: reached max steps without final response")

// ErrConsecutiveErrors 表示连续多轮工具调用全部失败,触发熔断。
var ErrConsecutiveErrors = errors.New("agent: too many consecutive tool errors, circuit broken")

// ErrPaused 在需要把「暂停」表示成 error 的边界使用(少见);正常路径用 RunOutcome.Status=Paused。
var ErrPaused = errors.New("agent: run paused; call Continue with resolutions")

// Requirement 是暂停时待人(或外置适配器)决议的一项(目前仅 confirmation / PermitAsk)。
type Requirement struct {
	ID       string
	ToolCall llm.ToolCall
	// Source 标明需求来自何处:"" 表示本 Runner;嵌套时为 "tool:name" / "chain:step" / "team:member" / "parallel:i"。
	Source string
}

// Resolution 是对某条 Requirement 的决议。
type Resolution struct {
	ID       string
	Approved bool
	Reason   string // 拒绝时作为工具结果喂回模型
}

// RunOutcome 是 Agent.Run / Continue 的统一结果。
type RunOutcome struct {
	Status           RunStatus
	RunID            string
	Response         *llm.Response // Done 时为终态;Paused 时为触发暂停的那轮模型输出
	Messages         []llm.Message // 截至当前的规范历史
	Requirements     []Requirement // Paused 时非空
	Err              error         // Status=Error 时非 nil
	CompletingMember string        // 编排器用:完成终态的成员名(Team 等)
	// Usage 是这次 run 里各次模型调用的用量合计,来自事件流,不从消息正文回推。
	Usage llm.Usage
}

// IsDone 表示已得到终态文本回复。
func (o RunOutcome) IsDone() bool { return o.Status == StatusDone }

// IsPaused 表示等待 Continue。
func (o RunOutcome) IsPaused() bool { return o.Status == StatusPaused }

// Final 在 Done 时返回 Response;Paused 返回 ErrPaused;Error 返回 o.Err。
func (o RunOutcome) Final() (*llm.Response, error) {
	switch o.Status {
	case StatusDone:
		return o.Response, nil
	case StatusPaused:
		return o.Response, fmt.Errorf("%w (run_id=%s)", ErrPaused, o.RunID)
	case StatusError:
		if o.Err != nil {
			return o.Response, o.Err
		}
		return o.Response, errors.New("agent: run error")
	default:
		return o.Response, fmt.Errorf("agent: unexpected status %q", o.Status)
	}
}

func outcomeDone(runID string, resp *llm.Response, msgs []llm.Message) RunOutcome {
	return RunOutcome{Status: StatusDone, RunID: runID, Response: resp, Messages: cloneMessages(msgs)}
}

func outcomePaused(runID string, resp *llm.Response, msgs []llm.Message, reqs []Requirement) RunOutcome {
	return RunOutcome{
		Status:       StatusPaused,
		RunID:        runID,
		Response:     resp,
		Messages:     cloneMessages(msgs),
		Requirements: reqs,
	}
}

func outcomeError(runID string, resp *llm.Response, msgs []llm.Message, err error) RunOutcome {
	return RunOutcome{Status: StatusError, RunID: runID, Response: resp, Messages: cloneMessages(msgs), Err: err}
}

func cloneMessages(msgs []llm.Message) []llm.Message {
	if msgs == nil {
		return nil
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	return out
}

// NestedPauseError 供 Tool.Call(如 AgentAsTool)向上冒泡子 Agent 的暂停。
// Resume 在父 Continue 时调用以恢复子 run(进程内;不进入 RunStore 序列化)。
type NestedPauseError struct {
	Child  RunOutcome
	Source string
	Resume func(ctx context.Context, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error]
}

func (e *NestedPauseError) Error() string {
	if e == nil {
		return "agent: nested pause"
	}
	return fmt.Sprintf("agent: nested pause from %q (run_id=%s)", e.Source, e.Child.RunID)
}

func (e *NestedPauseError) Is(target error) bool { return target == ErrPaused }

// CollectOutcome 消费 iter.Seq2[Event, error] 并收集为 RunOutcome(同步调用方使用)。
func CollectOutcome(seq iter.Seq2[Event, error]) RunOutcome {
	var last *llm.Response
	var msgs []llm.Message
	var usage llm.Usage
	var sawStep bool
	for ev, err := range seq {
		if err != nil {
			out := outcomeError("", last, msgs, err)
			out.Usage = usage
			return out
		}
		switch ev.Type {
		case EventFinal:
			out := outcomeDone(ev.RunID, ev.Response, msgs)
			out.Usage = usageFromSteps(sawStep, usage, ev.Response)
			return out
		case EventPaused:
			out := outcomePaused(ev.RunID, ev.Response, msgs, ev.Requirements)
			out.Usage = usageFromSteps(sawStep, usage, ev.Response)
			return out
		case EventError:
			out := outcomeError(ev.RunID, ev.Response, msgs, ev.Err)
			out.Usage = usageFromSteps(sawStep, usage, ev.Response)
			return out
		case EventStep:
			last = ev.Response
			if ev.Response != nil {
				usage = usage.Add(ev.Response.Usage)
				sawStep = true
			}
		}
	}
	out := outcomeDone("", last, msgs)
	out.Usage = usageFromSteps(sawStep, usage, last)
	return out
}

// usageFromSteps 在看到过 EventStep 时用各步之和;否则用终态响应上的用量。
// 终态响应经常重复最后一步,两边都加会重复计数。
func usageFromSteps(sawStep bool, stepSum llm.Usage, final *llm.Response) llm.Usage {
	if sawStep {
		return stepSum
	}
	if final != nil {
		return final.Usage
	}
	return llm.Usage{}
}
