package media

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
)

// Supervisor 监督一个子进程(典型:每路流的 ffmpeg 转码进程):非正常退出时按退避
// 重启,ctx 取消时优雅停止(SIGTERM → 宽限 → Kill)。
//
// 命令由 newCmd 工厂提供——每次(重)启动都新建一个 *exec.Cmd,ffmpeg 参数、输入输出
// 管道等 policy 全在工厂里,Supervisor 只管"跑起来 + 崩了重启 + 让停就停"。
//
// 典型用法:把它绑到 Hub 的 Session.Context(),该路流结束时自动停机。
//
//	sup := media.NewSupervisor(func() *exec.Cmd { return exec.Command("ffmpeg", args...) },
//	    media.WithSupervisorMetrics(hub.Metrics(), key))
//	go sup.Run(sess.Context())
type Supervisor struct {
	newCmd    func() *exec.Cmd
	policy    *backoff.Policy
	stopGrace time.Duration
	metrics   *Metrics
	name      string
}

// SupervisorOption 配置 Supervisor。
type SupervisorOption func(*Supervisor)

// WithRestartPolicy 设置重启退避策略(默认 base 500ms、max 30s、指数)。
func WithRestartPolicy(p *backoff.Policy) SupervisorOption {
	return func(s *Supervisor) {
		if p != nil {
			s.policy = p
		}
	}
}

// WithStopGrace 设置优雅停止的宽限时长(SIGTERM 后等这么久仍未退出则 Kill,默认 5s)。
func WithStopGrace(d time.Duration) SupervisorOption {
	return func(s *Supervisor) {
		if d > 0 {
			s.stopGrace = d
		}
	}
}

// WithSupervisorMetrics 让重启计入 OTel 指标(name 作为 stream 标签)。
func WithSupervisorMetrics(m *Metrics, name string) SupervisorOption {
	return func(s *Supervisor) {
		s.metrics = m
		s.name = name
	}
}

// NewSupervisor 创建进程监督器。
func NewSupervisor(newCmd func() *exec.Cmd, opts ...SupervisorOption) *Supervisor {
	s := &Supervisor{
		newCmd:    newCmd,
		policy:    backoff.New(backoff.WithBase(500*time.Millisecond), backoff.WithMax(30*time.Second)),
		stopGrace: 5 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run 启动并监督进程,阻塞直到 ctx 取消。进程自行退出(崩溃/结束)则按退避重启;
// ctx 取消时优雅停止当前进程并返回 nil。
func (s *Supervisor) Run(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return nil
		}
		cmd := s.newCmd()
		if err := cmd.Start(); err != nil {
			// 启动失败也按重启处理(如可执行文件暂时不可用)。
			if !s.waitBackoff(ctx, attempt) {
				return nil
			}
			continue
		}
		attempt = 0 // 成功启动后重置退避

		waitCh := make(chan error, 1)
		go func() { waitCh <- cmd.Wait() }()

		select {
		case <-ctx.Done():
			s.stop(cmd, waitCh)
			return nil
		case <-waitCh:
			// 进程自行退出 → 记一次重启并退避后重来。
			s.metrics.Restart(ctx, s.name)
			if !s.waitBackoff(ctx, attempt) {
				return nil
			}
		}
	}
}

// stop 优雅停止:先 SIGTERM,宽限内未退出则 Kill。
func (s *Supervisor) stop(cmd *exec.Cmd, waitCh <-chan error) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-waitCh:
	case <-time.After(s.stopGrace):
		_ = cmd.Process.Kill()
		<-waitCh
	}
}

// waitBackoff 等待第 attempt 次重启前的退避时长;ctx 取消返回 false。
func (s *Supervisor) waitBackoff(ctx context.Context, attempt int) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(s.policy.Duration(attempt)):
		return true
	}
}
