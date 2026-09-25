package console

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rushteam/beauty/pkg/foundation/deadlock"
)

var startTime = time.Now()

// registerBuiltins 注册内置命令与默认主题。
func (s *Server) registerBuiltins() {
	s.Register(&Command{
		Name: "help", Note: "列出所有可用命令", Example: "help",
		Flag: FlagPublic, Handler: s.cmdHelp,
	})
	s.Register(&Command{
		Name: "ping", Note: "连通性测试", Example: "ping",
		Flag: FlagPublic, Handler: cmdPing,
	})
	s.Register(&Command{
		Name: "env", Note: "运行环境与进程信息", Example: "env",
		Handler: cmdEnv,
	})
	s.Register(&Command{
		Name: "mem", Note: "内存与 GC 统计", Example: "mem",
		Handler: cmdMem,
	})
	s.Register(&Command{
		Name: "gc", Note: "手动触发一次 GC 并返回前后内存对比", Example: "gc",
		Handler: cmdGC,
	})
	s.Register(&Command{
		Name: "goroutine", Note: "当前 goroutine 数量", Example: "goroutine",
		Handler: cmdGoroutine,
	})
	s.Register(&Command{
		Name: "deadlock", Note: "检测疑似死锁/泄漏的 goroutine", Example: "deadlock [minMinutes]",
		Handler: cmdDeadlock,
	})
	s.Register(&Command{
		Name: "topics", Note: "列出可订阅的推送主题", Example: "topics",
		Flag: FlagPublic, Handler: s.cmdTopics,
	})
	s.Register(&Command{
		Name: "sub", Note: "订阅一个推送主题", Example: "sub top",
		Flag: FlagPublic, Handler: s.cmdSub,
	})
	s.Register(&Command{
		Name: "unsub", Note: "取消订阅一个推送主题", Example: "unsub top",
		Flag: FlagPublic, Handler: s.cmdUnsub,
	})

	// 默认主题:每 3 秒推送一行系统概览。
	s.RegisterTopic(&Topic{
		Name: "top", Note: "系统概览(goroutine/内存/GC),每 3 秒推送", Interval: 3 * time.Second,
		Build: buildTop,
	})
}

func (s *Server) cmdHelp(_ context.Context, c *Ctx) (string, error) {
	var b strings.Builder
	b.WriteString("可用命令:\n")
	for _, cmd := range s.listCommands() {
		if cmd.isInvisible() {
			continue
		}
		if !cmd.isPublic() && !c.Authorized {
			continue
		}
		tag := ""
		if cmd.isPublic() {
			tag = " (public)"
		}
		fmt.Fprintf(&b, "  %-12s %s%s\n", cmd.Name, cmd.Note, tag)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func cmdPing(_ context.Context, _ *Ctx) (string, error) { return "pong", nil }

func cmdEnv(_ context.Context, _ *Ctx) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "go version : %s\n", runtime.Version())
	fmt.Fprintf(&b, "os/arch    : %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "NumCPU     : %d\n", runtime.NumCPU())
	fmt.Fprintf(&b, "GOMAXPROCS : %d\n", runtime.GOMAXPROCS(0))
	fmt.Fprintf(&b, "goroutines : %d\n", runtime.NumGoroutine())
	fmt.Fprintf(&b, "uptime     : %s", time.Since(startTime).Round(time.Second))
	return b.String(), nil
}

func cmdMem(_ context.Context, _ *Ctx) (string, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return formatMem(&m), nil
}

func cmdGC(_ context.Context, _ *Ctx) (string, error) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	runtime.GC()
	runtime.ReadMemStats(&after)
	return fmt.Sprintf("GC 已触发\nHeapAlloc: %s -> %s\nNumGC    : %d -> %d",
		humanBytes(before.HeapAlloc), humanBytes(after.HeapAlloc),
		before.NumGC, after.NumGC), nil
}

func cmdGoroutine(_ context.Context, _ *Ctx) (string, error) {
	return fmt.Sprintf("goroutines: %d", runtime.NumGoroutine()), nil
}

func cmdDeadlock(_ context.Context, c *Ctx) (string, error) {
	minMinutes := 1
	if len(c.Args) >= 2 {
		if n, err := parseInt(c.Args[1]); err == nil {
			minMinutes = n
		}
	}
	rep, err := deadlock.Detect(
		deadlock.WithMinWaitMinutes(minMinutes),
		deadlock.WithIgnore("internal/poll.runtime_pollWait("),
	)
	if err != nil {
		return "", err
	}
	return rep.String(), nil
}

func (s *Server) cmdTopics(_ context.Context, _ *Ctx) (string, error) {
	s.mu.RLock()
	topics := make([]*Topic, 0, len(s.topics))
	for _, t := range s.topics {
		topics = append(topics, t)
	}
	s.mu.RUnlock()
	if len(topics) == 0 {
		return "(无可订阅主题)", nil
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	var b strings.Builder
	b.WriteString("可订阅主题:\n")
	for _, t := range topics {
		fmt.Fprintf(&b, "  %-12s %s (每 %s)\n", t.Name, t.Note, t.Interval)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (s *Server) cmdSub(_ context.Context, c *Ctx) (string, error) {
	if len(c.Args) < 2 {
		return "", fmt.Errorf("用法: sub <topic>")
	}
	name := c.Args[1]
	t := s.getTopic(name)
	if t == nil {
		return "", fmt.Errorf("未知主题: %s", name)
	}
	t.addSub(c.cl)
	return fmt.Sprintf("已订阅主题: %s", name), nil
}

func (s *Server) cmdUnsub(_ context.Context, c *Ctx) (string, error) {
	if len(c.Args) < 2 {
		return "", fmt.Errorf("用法: unsub <topic>")
	}
	name := c.Args[1]
	t := s.getTopic(name)
	if t == nil {
		return "", fmt.Errorf("未知主题: %s", name)
	}
	t.removeSub(c.cl)
	return fmt.Sprintf("已取消订阅: %s", name), nil
}

// buildTop 生成 top 主题的单行概览。
func buildTop() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return fmt.Sprintf("[%s] goroutines=%d heap=%s sys=%s numGC=%d uptime=%s",
		time.Now().Format("15:04:05"),
		runtime.NumGoroutine(),
		humanBytes(m.HeapAlloc),
		humanBytes(m.Sys),
		m.NumGC,
		time.Since(startTime).Round(time.Second),
	)
}

func formatMem(m *runtime.MemStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HeapAlloc  : %s\n", humanBytes(m.HeapAlloc))
	fmt.Fprintf(&b, "HeapInuse  : %s\n", humanBytes(m.HeapInuse))
	fmt.Fprintf(&b, "HeapObjects: %d\n", m.HeapObjects)
	fmt.Fprintf(&b, "StackInuse : %s\n", humanBytes(m.StackInuse))
	fmt.Fprintf(&b, "Sys        : %s\n", humanBytes(m.Sys))
	fmt.Fprintf(&b, "NumGC      : %d\n", m.NumGC)
	fmt.Fprintf(&b, "GCPause(last): %s", time.Duration(m.PauseNs[(m.NumGC+255)%256]))
	return b.String()
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func parseInt(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
