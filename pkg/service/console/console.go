// Package console 提供一个远程 Web 控制台服务,作为 beauty.Service 运行。
//
// 它在浏览器里呈现一个交互式命令行(通过 WebSocket 通信),用于线上调试与运维:
// 注册自定义命令、Tab 补全、历史命令重放(!n)、周期性数据推送(Topic),
// 并内置若干排障命令(help / env / mem / gc / goroutine / deadlock)。
//
// 设计要点:
//   - 纯 core 依赖:HTTP + github.com/coder/websocket(经 pkg/transport/ws 封装),不引入 contrib;
//   - 默认仅监听 127.0.0.1,避免误暴露到公网;需远程访问建议走 SSH 隧道或反向代理鉴权;
//   - 鉴权模型:未配置账号 => 开发模式(全部命令可执行);配置账号后,连接初始为未授权,
//     仅能执行 FlagPublic 命令,登录成功后方可执行受保护命令;
//   - 满足 beauty.Service + ReadyNotifier。
//
// 用法:
//
//	c := console.New(
//	    console.WithAddr("127.0.0.1:6070"),
//	    console.WithUser("admin", "secret"),
//	)
//	c.Register(&console.Command{
//	    Name: "hello", Note: "打个招呼", Flag: console.FlagPublic,
//	    Handler: func(ctx context.Context, cc *console.Ctx) (string, error) {
//	        return "hi " + strings.Join(cc.Args[1:], " "), nil
//	    },
//	})
//	app := beauty.New(beauty.WithService(c))
package console

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/transport/ws"
)

const (
	defaultAddr = "127.0.0.1:6070"
	defaultPath = "/console"
)

// Server 是远程 Web 控制台服务。零值不可用,请通过 New 创建。
type Server struct {
	addr    string
	path    string
	title   string
	users   map[string]string // user->password;为空表示开发模式(无需登录)
	origins []string

	mu       sync.RWMutex
	commands map[string]*Command
	topics   map[string]*Topic

	server *http.Server
	ready  chan struct{}
}

// Option 配置 Server。
type Option func(*Server)

// WithAddr 覆盖监听地址,默认 127.0.0.1:6060。
func WithAddr(addr string) Option { return func(s *Server) { s.addr = addr } }

// WithPath 设置控制台页面与 WebSocket 的基础路径,默认 /console。
// 页面为 {path},WebSocket 为 {path}/ws。
func WithPath(p string) Option {
	return func(s *Server) {
		if p != "" {
			s.path = "/" + strings.Trim(p, "/")
		}
	}
}

// WithTitle 设置控制台网页标题。
func WithTitle(t string) Option { return func(s *Server) { s.title = t } }

// WithUser 追加一个可登录账号。配置任意账号后即开启鉴权。
func WithUser(user, password string) Option {
	return func(s *Server) {
		if s.users == nil {
			s.users = make(map[string]string)
		}
		s.users[user] = password
	}
}

// WithUsers 批量设置可登录账号(覆盖已有)。
func WithUsers(users map[string]string) Option {
	return func(s *Server) {
		s.users = make(map[string]string, len(users))
		for k, v := range users {
			s.users[k] = v
		}
	}
}

// WithOriginPatterns 允许这些 origin 跨域连接 WebSocket(path.Match 模式)。
// 默认仅允许同源。
func WithOriginPatterns(patterns ...string) Option {
	return func(s *Server) { s.origins = patterns }
}

// WithCommand 在创建时注册一个命令(等价于 New 后调用 Register)。
func WithCommand(cmd *Command) Option {
	return func(s *Server) { s.Register(cmd) }
}

// WithTopic 在创建时注册一个周期推送主题。
func WithTopic(t *Topic) Option {
	return func(s *Server) { s.RegisterTopic(t) }
}

// New 创建控制台服务并注册内置命令与默认主题。
func New(opts ...Option) *Server {
	s := &Server{
		addr:     defaultAddr,
		path:     defaultPath,
		title:    "beauty console",
		commands: make(map[string]*Command),
		topics:   make(map[string]*Topic),
		ready:    make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	s.registerBuiltins()
	return s
}

// Register 注册(或覆盖)一个命令。返回 s 以便链式调用。并发安全。
func (s *Server) Register(cmd *Command) *Server {
	if cmd == nil || cmd.Name == "" || cmd.Handler == nil {
		return s
	}
	s.mu.Lock()
	s.commands[cmd.Name] = cmd
	s.mu.Unlock()
	return s
}

// RegisterTopic 注册(或覆盖)一个周期推送主题。返回 s 以便链式调用。
func (s *Server) RegisterTopic(t *Topic) *Server {
	if t == nil || t.Name == "" || t.Interval <= 0 || t.Build == nil {
		return s
	}
	if t.subs == nil {
		t.subs = make(map[*client]struct{})
	}
	s.mu.Lock()
	s.topics[t.Name] = t
	s.mu.Unlock()
	return s
}

func (s *Server) getCommand(name string) *Command {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.commands[name]
}

func (s *Server) getTopic(name string) *Topic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.topics[name]
}

// listCommands 返回按名称排序的命令快照。
func (s *Server) listCommands() []*Command {
	s.mu.RLock()
	out := make([]*Command, 0, len(s.commands))
	for _, c := range s.commands {
		out = append(out, c)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// authRequired 报告是否开启了鉴权(配置了账号)。
func (s *Server) authRequired() bool { return len(s.users) > 0 }

// checkLogin 校验账号密码。
func (s *Server) checkLogin(user, password string) bool {
	if !s.authRequired() {
		return true
	}
	want, ok := s.users[user]
	return ok && want == password
}

// Handler 把控制台的页面与 WebSocket 路由注册到给定 mux,便于挂载到已有 Web 服务。
// 若不使用独立服务(Start),可用此方法嵌入 webserver 的 mux。
func (s *Server) Handler(mux *http.ServeMux) {
	mux.HandleFunc(s.path, s.handlePage)
	mux.Handle(s.path+"/ws", ws.Handler(s.handleWS, s.wsOptions()...))
}

func (s *Server) wsOptions() []ws.Option {
	var opts []ws.Option
	if len(s.origins) > 0 {
		opts = append(opts, ws.WithOriginPatterns(s.origins...))
	}
	// 控制台连接长期空闲后由前端心跳保活;这里加服务端 ping 兜底检测半开连接。
	opts = append(opts, ws.WithPingInterval(30*time.Second))
	return opts
}

// Start 实现 beauty.Service。启动独立 HTTP 服务并运行所有主题的推送循环。
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()

	mux := http.NewServeMux()
	s.Handler(mux)
	s.server = &http.Server{
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
	}

	// 启动主题推送循环,ctx 取消时统一退出。
	s.mu.RLock()
	topics := make([]*Topic, 0, len(s.topics))
	for _, t := range s.topics {
		topics = append(topics, t)
	}
	s.mu.RUnlock()
	for _, t := range topics {
		go t.run(ctx)
	}

	slog.Info("console server listening",
		"url", "http://"+s.addr+s.path, "auth", s.authRequired())
	close(s.ready)

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("console server error", "err", err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.server.Shutdown(shutCtx)
}

// Ready 实现 beauty.ReadyNotifier。
func (s *Server) Ready() <-chan struct{} { return s.ready }

func (s *Server) String() string { return "console@" + s.addr }
