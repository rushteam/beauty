// Package llmservice 把 contrib/llm/agent.Agent 包装为 beauty.Service,
// 使 Agent 的长生命周期循环(tool loop、HITL Continue)接入 beauty 的优雅停机、
// 注册发现和排空语义——和 HTTP/gRPC/cron 等服务共享同一停机顺序:
//
//	注销 → drain → 取消进行中 Run → flush checkpoint → 退出
//
// 同时提供:
//   - Worker 池:并发消费任务(来自 HTTP/MQ/外部投递)
//   - ReadyNotifier:worker 就绪后才注册
//   - SSE+Continue HTTP Handler:直接暴露或挂到 WebServer mux
//   - 分布式锁(pkg/dlock):同一 run_id 只有一个 worker 续跑
//   - 亲和路由(pkg/shard):多节点按 session 做一致性哈希
//
// 用法:
//
//	app := beauty.New(
//	    beauty.WithRegistry(etcd),
//	    beauty.WithWebServer(":8080", mux),
//	    llmservice.WithAgent("reviewer", runner, llmservice.Workers(4)),
//	)
//	app.Start(ctx)
package llmservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/httpui"
	"github.com/rushteam/beauty/pkg/dlock"
	"github.com/rushteam/beauty/pkg/shard"
)

var _ beauty.Service = (*AgentService)(nil)
var _ beauty.ReadyNotifier = (*AgentService)(nil)

// AgentService 把一个 Agent 包成 beauty.Service,在 Start 内跑 worker 池消费任务。
type AgentService struct {
	name    string
	agent   agent.Agent
	store   agent.RunStore
	workers int
	locker  dlock.Locker
	sharder *shard.Sharder

	ready   chan struct{}
	tasks   chan Task
	taskBuf int

	running sync.WaitGroup
	cancel  context.CancelFunc
	stopped atomic.Bool
}

// Task 是提交给 AgentService 的一次运行请求。
type Task struct {
	// RunID 非空时表示 Continue;空时表示新 Run。
	RunID       string             `json:"run_id,omitempty"`
	Request     llm.Request        `json:"request"`
	Resolutions []agent.Resolution `json:"resolutions,omitempty"`

	// Callback 在任务完成时异步回调(可空)。不参与 JSON 序列化。
	Callback func(agent.RunOutcome) `json:"-"`
}

// New 创建 AgentService。
func New(name string, a agent.Agent, opts ...ServiceOption) *AgentService {
	s := &AgentService{
		name:    name,
		agent:   a,
		workers: 2,
		taskBuf: 64,
		ready:   make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	if s.tasks == nil {
		s.tasks = make(chan Task, s.taskBuf)
	}
	return s
}

// Submit 向 worker 池投递一个任务。阻塞直到有空间或 ctx 取消。
func (s *AgentService) Submit(ctx context.Context, t Task) error {
	if s.stopped.Load() {
		return fmt.Errorf("llmservice: %s is stopped", s.name)
	}
	select {
	case s.tasks <- t:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Handler 返回 HTTP handler,路由: POST /run, POST /continue, GET /events。
// 如果配置了 Sharder,非本节点归属的请求会被反向代理到正确节点(粘性会话)。
func (s *AgentService) Handler() http.Handler {
	inner := &httpui.Handler{Agent: s.agent, Name: s.name, Store: s.store}
	if s.sharder == nil {
		return inner
	}
	return shard.NewRouter(s.sharder, sessionKeyFromRequest, inner)
}

func sessionKeyFromRequest(r *http.Request) string {
	if sid := r.URL.Query().Get("session_id"); sid != "" {
		return sid
	}
	if sid := r.Header.Get("X-Session-ID"); sid != "" {
		return sid
	}
	return ""
}

// Start 实现 beauty.Service。启动 worker 池,ctx 取消时排空正在执行的 Run。
func (s *AgentService) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()

	// 启动 workers
	for i := range s.workers {
		s.running.Add(1)
		go s.worker(ctx, i)
	}

	// 就绪
	close(s.ready)

	// 阻塞直到 ctx 取消
	<-ctx.Done()

	// 停机:不再接受新任务
	s.stopped.Store(true)

	// 排空 task channel 中已排队但未被消费的任务(标记取消)
	close(s.tasks)

	// 等待所有正在执行的 Run 完成(含 checkpoint flush)
	s.running.Wait()
	return nil
}

// Ready 实现 beauty.ReadyNotifier。
func (s *AgentService) Ready() <-chan struct{} { return s.ready }

// String 实现 beauty.Service。
func (s *AgentService) String() string { return "llmservice.Agent(" + s.name + ")" }

func (s *AgentService) worker(ctx context.Context, id int) {
	defer s.running.Done()
	_ = id
	for task := range s.tasks {
		if ctx.Err() != nil {
			if task.Callback != nil {
				task.Callback(agent.RunOutcome{Status: agent.StatusError, Err: ctx.Err()})
			}
			continue
		}
		s.executeTask(ctx, task)
	}
}

func (s *AgentService) executeTask(ctx context.Context, t Task) {
	runID := t.RunID

	// 分布式锁:同一 run_id 只有一个 worker 续跑
	if s.locker != nil && runID != "" {
		lk, err := s.locker.Lock(ctx, "llmservice:run:"+runID)
		if err != nil {
			if t.Callback != nil {
				t.Callback(agent.RunOutcome{Status: agent.StatusError, Err: err})
			}
			return
		}
		defer lk.Unlock(context.Background())
	}

	var outcome agent.RunOutcome
	if runID != "" {
		outcome = agent.CollectOutcome(s.agent.Continue(ctx, runID, t.Resolutions))
	} else {
		outcome = agent.CollectOutcome(s.agent.Run(ctx, t.Request))
	}

	if t.Callback != nil {
		t.Callback(outcome)
	}
}

// WithAgent 创建 beauty.Option,把 Agent 作为 Service 注册到 App。
func WithAgent(name string, a agent.Agent, opts ...ServiceOption) beauty.Option {
	return beauty.WithService(New(name, a, opts...))
}

// ---- HTTP 快捷挂载 ----

// MountTo 把 SSE handler 挂到 mux 上(路径前缀 /agents/{name}/)。
func (s *AgentService) MountTo(mux *http.ServeMux) {
	prefix := "/agents/" + s.name
	mux.Handle(prefix+"/run", http.StripPrefix(prefix, s.Handler()))
	mux.Handle(prefix+"/continue", http.StripPrefix(prefix, s.Handler()))
	mux.Handle(prefix+"/events", http.StripPrefix(prefix, s.Handler()))
}

// ---- 任务反序列化(供 MQ handler 使用) ----

// TaskFromMessage 从 mq.Message 反序列化 Task(Body 为 JSON)。
func TaskFromMessage(body []byte) (Task, error) {
	var t Task
	err := json.Unmarshal(body, &t)
	return t, err
}
