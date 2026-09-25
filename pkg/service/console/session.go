package console

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/rushteam/beauty/pkg/transport/ws"
)

// client 封装单个 WebSocket 连接,串行化写操作(命令响应与主题推送可能并发写)。
type client struct {
	conn *ws.Conn
	mu   sync.Mutex
}

func (cl *client) write(ctx context.Context, resp response) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.conn.WriteJSON(ctx, resp)
}

// request 是前端 -> 服务端的消息。
type request struct {
	ID       int64  `json:"id"`
	Action   string `json:"action"` // command | hint | login
	Command  string `json:"command,omitempty"`
	Head     string `json:"head,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// response 是服务端 -> 前端的消息。
type response struct {
	ID    int64  `json:"id"`
	OK    bool   `json:"ok"`
	Type  string `json:"type"`            // text | hint | login | push | welcome
	Data  any    `json:"data,omitempty"`  // 文本结果 / 提示列表 / 推送内容
	Error string `json:"error,omitempty"` // OK=false 时的错误信息
	Push  string `json:"push,omitempty"`  // type=push 时的主题名
}

// hintItem 是一条补全提示。
type hintItem struct {
	Name    string `json:"name"`
	Example string `json:"example"`
	Note    string `json:"note"`
}

// handleWS 是单个 WebSocket 连接的主循环。
func (s *Server) handleWS(r *http.Request, c *ws.Conn) error {
	ctx := r.Context()
	cl := &client{conn: c}
	authorized := !s.authRequired() // 未配置账号 => 开发模式,默认已授权
	remote := r.RemoteAddr

	// 连接断开后从所有主题退订,避免向已关闭连接推送。
	defer s.dropClient(cl)

	// 欢迎横幅。
	_ = cl.write(ctx, response{OK: true, Type: "welcome", Data: s.welcome(authorized)})

	for {
		var req request
		if err := c.ReadJSON(ctx, &req); err != nil {
			return nil // 连接关闭或出错,正常退出
		}
		switch req.Action {
		case "login":
			if s.checkLogin(req.User, req.Password) {
				authorized = true
				_ = cl.write(ctx, response{ID: req.ID, OK: true, Type: "login", Data: "登录成功"})
			} else {
				_ = cl.write(ctx, response{ID: req.ID, OK: false, Type: "login", Error: "用户名或密码错误"})
			}
		case "hint":
			_ = cl.write(ctx, response{ID: req.ID, OK: true, Type: "hint", Data: s.hint(req.Head, authorized)})
		case "command":
			out, err := s.runCommand(ctx, req.Command, authorized, remote, cl)
			if err != nil {
				_ = cl.write(ctx, response{ID: req.ID, OK: false, Type: "text", Error: err.Error()})
			} else {
				_ = cl.write(ctx, response{ID: req.ID, OK: true, Type: "text", Data: out})
			}
		default:
			_ = cl.write(ctx, response{ID: req.ID, OK: false, Error: "未知指令: " + req.Action})
		}
	}
}

// runCommand 解析并执行一行命令。
func (s *Server) runCommand(ctx context.Context, line string, authorized bool, remote string, cl *client) (out string, err error) {
	args := strings.Fields(strings.TrimSpace(line))
	if len(args) == 0 {
		return "", nil
	}
	name := args[0]
	cmd := s.getCommand(name)
	if cmd == nil {
		return "", fmt.Errorf("未知命令: %s (输入 help 查看可用命令)", name)
	}
	// 未开启鉴权时(开发模式)一律视为已授权。
	authorized = authorized || !s.authRequired()
	if !cmd.isPublic() && !authorized {
		return "", fmt.Errorf("需要登录后执行: %s", name)
	}

	// 防止业务命令 panic 拖垮整个连接循环。
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("console command panic", "cmd", name, "recover", rec)
			err = fmt.Errorf("命令执行 panic: %v\n%s", rec, debug.Stack())
		}
	}()

	return cmd.Handler(ctx, &Ctx{
		Args:       args,
		Authorized: authorized,
		Remote:     remote,
		srv:        s,
		cl:         cl,
	})
}

// hint 根据已输入前缀返回补全提示(按名称排序)。
func (s *Server) hint(head string, authorized bool) []hintItem {
	head = strings.TrimSpace(head)
	items := make([]hintItem, 0, 8)
	for _, cmd := range s.listCommands() {
		if cmd.isInvisible() {
			continue
		}
		if !cmd.isPublic() && !authorized {
			continue
		}
		if strings.HasPrefix(cmd.Name, head) {
			items = append(items, hintItem{Name: cmd.Name, Example: cmd.Example, Note: cmd.Note})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// welcome 生成连接建立时的欢迎文本。
func (s *Server) welcome(authorized bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", s.title)
	if s.authRequired() && !authorized {
		b.WriteString("提示: 已开启鉴权,请先使用界面登录后执行受保护命令。\n")
	}
	b.WriteString("输入 help 查看可用命令。")
	return b.String()
}

// dropClient 把连接从所有主题的订阅集合中移除。
func (s *Server) dropClient(cl *client) {
	s.mu.RLock()
	topics := make([]*Topic, 0, len(s.topics))
	for _, t := range s.topics {
		topics = append(topics, t)
	}
	s.mu.RUnlock()
	for _, t := range topics {
		t.removeSub(cl)
	}
}
