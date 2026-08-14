package checkpoint

import (
	"github.com/rushteam/beauty/contrib/llm"
)

// ReplayMessages 从 checkpoint 事件序列重建对话 Messages(用于 Pause 恢复)。
// 只投影模型可见的消息:user/assistant(tool_calls)/tool/steer。
func ReplayMessages(events []Event) []llm.Message {
	var msgs []llm.Message
	for _, ev := range events {
		switch ev.Type {
		case TypeUserMessage, TypeSteerMessage:
			if ev.Message != nil {
				msgs = append(msgs, *ev.Message)
			} else if ev.Result != "" {
				msgs = append(msgs, llm.Message{Role: llm.User, Content: ev.Result})
			}
		case TypeModelResponse:
			if ev.Response == nil {
				continue
			}
			if len(ev.Response.ToolCalls) > 0 {
				msgs = append(msgs, llm.Message{
					Role:      llm.Assistant,
					Content:   ev.Response.Content,
					ToolCalls: ev.Response.ToolCalls,
				})
			}
		case TypeToolResult:
			id := ""
			if ev.ToolCall != nil {
				id = ev.ToolCall.ID
			}
			if id != "" {
				msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: id, Content: ev.Result})
			}
		}
	}
	return msgs
}

// RunNode 是 sub-agent 编排树的一个节点(可视化)。
type RunNode struct {
	RunID       string     `json:"run_id"`
	ParentRunID string     `json:"parent_run_id,omitempty"`
	AgentName   string     `json:"agent_name,omitempty"`
	Depth       int        `json:"depth"`
	Status      string     `json:"status"`
	Source      string     `json:"source,omitempty"`
	Children    []*RunNode `json:"children,omitempty"`
}

// BuildRunTree 从事件日志构建 sub-agent 编排树。
func BuildRunTree(events []Event) *RunNode {
	if len(events) == 0 {
		return nil
	}
	nodes := map[string]*RunNode{}
	var root *RunNode

	ensure := func(runID, parent, agent string, depth int) *RunNode {
		if n, ok := nodes[runID]; ok {
			if agent != "" && n.AgentName == "" {
				n.AgentName = agent
			}
			return n
		}
		n := &RunNode{RunID: runID, ParentRunID: parent, AgentName: agent, Depth: depth, Status: "running"}
		nodes[runID] = n
		if parent == "" && root == nil {
			root = n
		} else if parent != "" {
			if p, ok := nodes[parent]; ok {
				p.Children = append(p.Children, n)
			}
		}
		return n
	}

	for _, ev := range events {
		n := ensure(ev.RunID, ev.ParentRunID, ev.AgentName, ev.Depth)
		switch ev.Type {
		case TypeRunStarted:
			n.Status = "running"
		case TypeAgentSpawned:
			if ev.ChildRunID != "" {
				child := ensure(ev.ChildRunID, ev.RunID, "", ev.Depth+1)
				child.Source = ev.Source
				child.ParentRunID = ev.RunID
			}
		case TypeRunPaused:
			n.Status = "paused"
		case TypeRunCompleted, TypeAgentCompleted:
			n.Status = "done"
		case TypeRunError:
			n.Status = "error"
		}
	}
	if root == nil && len(events) > 0 {
		ev := events[0]
		root = ensure(ev.RunID, ev.ParentRunID, ev.AgentName, ev.Depth)
	}
	return root
}
