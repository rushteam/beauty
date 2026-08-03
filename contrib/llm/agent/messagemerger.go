package agent

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// ==== 相邻同角色消息合并 ====
//
// 某些 provider 要求消息角色严格交替(user/assistant 相间),或在注入 system 背景、Steer 插话、
// 各类 nudge 之后,请求里出现了连续多条同角色消息。MergeConsecutive 把相邻且同为
// system/user/assistant 的消息合并成一条:文本用 sep 连接,ToolCalls 顺序拼接,Parts 顺序拼接。
//
// Role=Tool 的消息**永不合并**——它靠 ToolCallID 与某次具体调用一一对应,合并会破坏语义。
//
// 机制而非策略:是否需要合并、用什么分隔符,由使用方决定(常见做法是把 MergeMessagesHook 挂到
// Runner.Hooks.BeforeModel,在每轮请求发出前规整)。

// MergeConsecutive 合并 msgs 中相邻且同角色(system/user/assistant)的消息,返回新切片,
// 不改动入参底层数组。sep 为空时用 "\n\n" 连接文本。
func MergeConsecutive(msgs []llm.Message, sep string) []llm.Message {
	if sep == "" {
		sep = "\n\n"
	}
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if n := len(out); n > 0 && mergeable(out[n-1], m) {
			out[n-1] = mergeInto(out[n-1], m, sep)
			continue
		}
		out = append(out, cloneMessage(m))
	}
	return out
}

// MergeMessagesHook 返回一个把 MergeConsecutive 作用于每轮请求消息的 BeforeModel Hook。
// 用法:runner.Hooks.BeforeModel = agent.MergeMessagesHook("")。
func MergeMessagesHook(sep string) func(ctx context.Context, step int, req *llm.Request) error {
	return func(_ context.Context, _ int, req *llm.Request) error {
		req.Messages = MergeConsecutive(req.Messages, sep)
		return nil
	}
}

// mergeable 判断相邻两条消息是否可合并:同角色,且都不是 Tool 消息。
func mergeable(a, b llm.Message) bool {
	return a.Role == b.Role && a.Role != llm.Tool
}

// mergeInto 把 b 合并进 a 的副本:文本用 sep 连接(两边都非空时),Parts/ToolCalls 顺序拼接。
func mergeInto(a, b llm.Message, sep string) llm.Message {
	m := cloneMessage(a)
	switch {
	case m.Content == "":
		m.Content = b.Content
	case b.Content != "":
		m.Content += sep + b.Content
	}
	if len(b.Parts) > 0 {
		m.Parts = append(m.Parts, cloneParts(b.Parts)...)
	}
	if len(b.ToolCalls) > 0 {
		m.ToolCalls = append(m.ToolCalls, b.ToolCalls...)
	}
	return m
}

// cloneMessage 深拷贝会被追加的切片字段,避免合并时改到调用方底层数组。
func cloneMessage(m llm.Message) llm.Message {
	c := m
	c.Parts = cloneParts(m.Parts)
	if len(m.ToolCalls) > 0 {
		c.ToolCalls = append([]llm.ToolCall(nil), m.ToolCalls...)
	}
	return c
}

func cloneParts(ps []llm.Part) []llm.Part {
	if len(ps) == 0 {
		return nil
	}
	return append([]llm.Part(nil), ps...)
}
