package agent

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// ---- HistoryProvider / ContextProvider 分离 ----
//
// HistoryProvider 职责:会话历史的加载与持久化。
// ContextProvider 职责:RAG、Skills、环境信息等临时上下文注入(可注入工具)。
// 两者 Invoking/Invoked 生命周期对称,在 runner 的不同阶段被调用:
//   HistoryProvider.Invoking → 前插历史
//   ContextProvider.Invoking → 追加上下文 + 注入临时工具
//   [agent loop]
//   ContextProvider.Invoked → 上下文回写(可选)
//   HistoryProvider.Invoked → 历史持久化(仅成功时)

// HistoryProvider 在运行前加载历史消息,运行后持久化新消息。
type HistoryProvider interface {
	// Invoking 在 agent loop 开始前调用。返回要前插的历史消息和可选的 system 补充。
	// 实现应排除 Source 为自身注入的消息(通过 SourceHistory 过滤)。
	Invoking(ctx context.Context, sessionID string) (messages []llm.Message, systemExtra string, err error)

	// Invoked 在 agent loop 成功完成后调用,持久化本轮新增消息。
	// 出错时不调用(避免持久化不完整的对话)。
	// newMessages 已排除 SourceHistory 来源(不重复存储历史注入的消息)。
	Invoked(ctx context.Context, sessionID string, newMessages []llm.Message) error
}

// ContextProvider 在运行前注入上下文消息和临时工具,运行后可选回写。
type ContextProvider interface {
	// Invoking 在 HistoryProvider 之后、agent loop 之前调用。
	// 返回要追加的上下文消息、可选的 system 补充、以及临时工具。
	// 消息会被自动标记为 SourceContext。
	Invoking(ctx context.Context, req *llm.Request) (messages []llm.Message, tools []Tool, err error)

	// Invoked 在 agent loop 完成后调用(包括成功和失败)。
	// 用于清理临时状态或回写上下文(如更新 RAG 索引)。
	Invoked(ctx context.Context, outcome *RunOutcome) error
}

// HistoryProviderFunc 是 HistoryProvider 的函数适配器(仅 Invoking;Invoked 空操作)。
type HistoryProviderFunc func(ctx context.Context, sessionID string) ([]llm.Message, string, error)

func (f HistoryProviderFunc) Invoking(ctx context.Context, sessionID string) ([]llm.Message, string, error) {
	return f(ctx, sessionID)
}

func (f HistoryProviderFunc) Invoked(ctx context.Context, sessionID string, newMessages []llm.Message) error {
	return nil
}

// ContextProviderFunc 是 ContextProvider 的函数适配器(仅 Invoking;Invoked 空操作)。
type ContextProviderFunc func(ctx context.Context, req *llm.Request) ([]llm.Message, []Tool, error)

func (f ContextProviderFunc) Invoking(ctx context.Context, req *llm.Request) ([]llm.Message, []Tool, error) {
	return f(ctx, req)
}

func (f ContextProviderFunc) Invoked(ctx context.Context, outcome *RunOutcome) error {
	return nil
}

// RAGContextProvider 是一个通用 RAG 上下文注入器,在每次运行前用查询函数检索相关文档。
func RAGContextProvider(retrieve func(ctx context.Context, query string) ([]string, error)) ContextProvider {
	return ContextProviderFunc(func(ctx context.Context, req *llm.Request) ([]llm.Message, []Tool, error) {
		query := lastUserContent(req.Messages)
		if query == "" {
			return nil, nil, nil
		}
		docs, err := retrieve(ctx, query)
		if err != nil {
			return nil, nil, err
		}
		if len(docs) == 0 {
			return nil, nil, nil
		}
		var content string
		for i, doc := range docs {
			if i > 0 {
				content += "\n---\n"
			}
			content += doc
		}
		msgs := []llm.Message{{
			Role:    llm.User,
			Content: "以下是与你的问题相关的参考资料:\n\n" + content,
			Source:  llm.SourceContext,
		}}
		return msgs, nil, nil
	})
}

func lastUserContent(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.User && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// InMemoryHistoryProvider 基于 session Store 的内存历史管理。
type InMemoryHistoryProvider struct {
	store map[string]*historyEntry
}

type historyEntry struct {
	messages []llm.Message
	summary  string
}

// NewInMemoryHistoryProvider 创建内存历史 provider(测试/单机)。
func NewInMemoryHistoryProvider() *InMemoryHistoryProvider {
	return &InMemoryHistoryProvider{store: make(map[string]*historyEntry)}
}

func (p *InMemoryHistoryProvider) Invoking(_ context.Context, sessionID string) ([]llm.Message, string, error) {
	entry, ok := p.store[sessionID]
	if !ok {
		return nil, "", nil
	}
	msgs := make([]llm.Message, len(entry.messages))
	for i, m := range entry.messages {
		m.Source = llm.SourceHistory
		msgs[i] = m
	}
	systemExtra := ""
	if entry.summary != "" {
		systemExtra = "以下是此前对话的摘要:\n" + entry.summary
	}
	return msgs, systemExtra, nil
}

func (p *InMemoryHistoryProvider) Invoked(_ context.Context, sessionID string, newMessages []llm.Message) error {
	entry, ok := p.store[sessionID]
	if !ok {
		entry = &historyEntry{}
		p.store[sessionID] = entry
	}
	entry.messages = append(entry.messages, llm.Persistable().Apply(newMessages)...)
	return nil
}

// FilteringHistoryProvider 用可配置的消息 filter 包装 HistoryProvider。
type FilteringHistoryProvider struct {
	Inner       HistoryProvider
	LoadFilter  llm.MessageFilter // Invoking 加载后应用(nil = 不过滤)
	StoreFilter llm.MessageFilter // Invoked 持久化前应用(nil = Persistable)
}

func (p *FilteringHistoryProvider) Invoking(ctx context.Context, sessionID string) ([]llm.Message, string, error) {
	msgs, systemExtra, err := p.Inner.Invoking(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	if p.LoadFilter != nil {
		msgs = p.LoadFilter.Apply(msgs)
	}
	return msgs, systemExtra, nil
}

func (p *FilteringHistoryProvider) Invoked(ctx context.Context, sessionID string, newMessages []llm.Message) error {
	storeFilter := p.StoreFilter
	if storeFilter == nil {
		storeFilter = llm.Persistable()
	}
	return p.Inner.Invoked(ctx, sessionID, storeFilter.Apply(newMessages))
}

// FilteringContextProvider 用消息 filter 包装 ContextProvider,过滤注入的上下文消息。
type FilteringContextProvider struct {
	Inner        ContextProvider
	InjectFilter llm.MessageFilter // Invoking 注入前应用(nil = 不过滤)
}

func (p *FilteringContextProvider) Invoking(ctx context.Context, req *llm.Request) ([]llm.Message, []Tool, error) {
	msgs, tools, err := p.Inner.Invoking(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if p.InjectFilter != nil {
		msgs = p.InjectFilter.Apply(msgs)
	}
	return msgs, tools, nil
}

func (p *FilteringContextProvider) Invoked(ctx context.Context, outcome *RunOutcome) error {
	return p.Inner.Invoked(ctx, outcome)
}

// WithLoadFilter 包装 HistoryProvider,在加载历史时过滤消息。
func WithLoadFilter(hp HistoryProvider, f llm.MessageFilter) HistoryProvider {
	if fp, ok := hp.(*FilteringHistoryProvider); ok {
		return &FilteringHistoryProvider{
			Inner:       fp.Inner,
			LoadFilter:  f,
			StoreFilter: fp.StoreFilter,
		}
	}
	return &FilteringHistoryProvider{Inner: hp, LoadFilter: f}
}

// WithStoreFilter 包装 HistoryProvider,在持久化前过滤消息。
func WithStoreFilter(hp HistoryProvider, f llm.MessageFilter) HistoryProvider {
	if fp, ok := hp.(*FilteringHistoryProvider); ok {
		return &FilteringHistoryProvider{
			Inner:       fp.Inner,
			LoadFilter:  fp.LoadFilter,
			StoreFilter: f,
		}
	}
	return &FilteringHistoryProvider{Inner: hp, StoreFilter: f}
}
