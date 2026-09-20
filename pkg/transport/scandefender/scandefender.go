// Package scandefender 提供 IP 级连接频率与闪断检测,自动封禁可疑扫描器。
//
// 在生产环境中,暴露在公网的 TCP/WebSocket 端口会遭到大量端口扫描与探测请求。
// ScanDefender 在 Accept 层做前置过滤,检测两类异常行为:
//   - **高频连接**:同一 IP 在窗口时间内连接次数超阈值(如 10 次/分钟);
//   - **闪断**(flash disconnect):连接建立后极短时间内(如 <1s)断开,
//     同一 IP 累计闪断次数超阈值(如 5 次/分钟)。
//
// 触发封禁后,该 IP 在封禁期内的所有连接请求将被直接拒绝(返回 ErrBanned)。
// 封禁到期后自动解除。
//
// 并发安全。适合在连接建立(Accept)后立即调用 Admit,通过 OnClose 回调
// 报告连接结束;也可作为 HTTP 中间件使用。
//
// 用法:
//
//	sd := scandefender.New(
//	    scandefender.WithConnRate(10, time.Minute),
//	    scandefender.WithFlashThreshold(5, time.Second),
//	    scandefender.WithBanDuration(5*time.Minute),
//	)
//
//	// TCP accept:
//	if err := sd.Admit(conn.RemoteAddr().String()); err != nil {
//	    conn.Close()
//	    return
//	}
//	defer sd.OnClose(conn.RemoteAddr().String())
//
//	// 或作为 HTTP 中间件:
//	mux.Handle("/ws", sd.Middleware(wsHandler))
package scandefender

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// ErrBanned 表示该 IP 已被封禁。
var ErrBanned = errors.New("scandefender: ip banned")

// Defender 追踪每个 IP 的连接行为并自动封禁可疑扫描器。
type Defender struct {
	cfg config
	mu  sync.Mutex
	ips map[string]*ipState
}

type ipState struct {
	conns      []time.Time // 连接时间戳
	flashes    []time.Time // 闪断时间戳
	bannedUtil time.Time   // 封禁到期时间
}

type config struct {
	connRate       int           // 窗口内最大连接次数
	connWindow     time.Duration // 连接计数窗口
	flashThreshold int           // 窗口内最大闪断次数
	flashDuration  time.Duration // "闪断"的定义:连接持续时间 < 此值
	flashWindow    time.Duration // 闪断计数窗口
	banDuration    time.Duration // 封禁时长
	cleanInterval  time.Duration // 过期条目清理间隔
}

// Option 配置 Defender。
type Option func(*config)

// WithConnRate 设置连接频率阈值:窗口 window 内超过 max 次连接即封禁。
// 默认 20 次/分钟。
func WithConnRate(max int, window time.Duration) Option {
	return func(c *config) { c.connRate = max; c.connWindow = window }
}

// WithFlashThreshold 设置闪断阈值:连接持续时间 < flashDur 视为一次闪断,
// 窗口 window 内累计 max 次即封禁。默认 5 次/分钟,闪断定义 < 2s。
func WithFlashThreshold(max int, flashDur time.Duration) Option {
	return func(c *config) { c.flashThreshold = max; c.flashDuration = flashDur }
}

// WithFlashWindow 设置闪断统计窗口。默认 1 分钟。
func WithFlashWindow(d time.Duration) Option {
	return func(c *config) { c.flashWindow = d }
}

// WithBanDuration 设置封禁时长。默认 5 分钟。
func WithBanDuration(d time.Duration) Option {
	return func(c *config) { c.banDuration = d }
}

// New 创建 Defender。
func New(opts ...Option) *Defender {
	cfg := config{
		connRate:       20,
		connWindow:     time.Minute,
		flashThreshold: 5,
		flashDuration:  2 * time.Second,
		flashWindow:    time.Minute,
		banDuration:    5 * time.Minute,
		cleanInterval:  10 * time.Minute,
	}
	for _, o := range opts {
		o(&cfg)
	}
	d := &Defender{
		cfg: cfg,
		ips: make(map[string]*ipState),
	}
	return d
}

// Admit 在连接建立时调用。返回 nil 表示放行,ErrBanned 表示拒绝。
// addr 应为 "ip:port" 格式(net.Addr.String()),自动提取 IP 部分。
func (d *Defender) Admit(addr string) error {
	ip := extractIP(addr)
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.getOrCreate(ip)

	// 封禁中?
	if now.Before(st.bannedUtil) {
		return ErrBanned
	}

	// 记录连接
	st.conns = appendAndTrim(st.conns, now, d.cfg.connWindow)

	// 检查连接频率
	if len(st.conns) > d.cfg.connRate {
		st.bannedUtil = now.Add(d.cfg.banDuration)
		return ErrBanned
	}

	return nil
}

// OnClose 在连接关闭时调用,用于闪断检测。
// connectedAt 是连接建立的时间;addr 同 Admit 的参数。
func (d *Defender) OnClose(addr string, connectedAt time.Time) {
	ip := extractIP(addr)
	now := time.Now()

	// 判断是否闪断
	if now.Sub(connectedAt) >= d.cfg.flashDuration {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.getOrCreate(ip)
	if now.Before(st.bannedUtil) {
		return
	}

	st.flashes = appendAndTrim(st.flashes, now, d.cfg.flashWindow)

	if len(st.flashes) > d.cfg.flashThreshold {
		st.bannedUtil = now.Add(d.cfg.banDuration)
	}
}

// IsBanned 检查某 IP 当前是否被封禁。addr 可为 "ip:port" 或纯 IP。
func (d *Defender) IsBanned(addr string) bool {
	ip := extractIP(addr)
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.ips[ip]
	if !ok {
		return false
	}
	return time.Now().Before(st.bannedUtil)
}

// OnHandshake 在连接成功完成业务握手后调用,重置该 IP 的连接计数器。
// 解决同一出口 IP(如公司/校园网关)多人合法连接被误封的问题:
// 只有未完成握手的连接才计入频率检测;成功握手说明是合法客户端。
func (d *Defender) OnHandshake(addr string) {
	ip := extractIP(addr)
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.ips[ip]
	if !ok {
		return
	}
	st.conns = st.conns[:0]
	st.flashes = st.flashes[:0]
}

// Unban 手动解封一个 IP。
func (d *Defender) Unban(addr string) {
	ip := extractIP(addr)
	d.mu.Lock()
	defer d.mu.Unlock()
	if st, ok := d.ips[ip]; ok {
		st.bannedUtil = time.Time{}
	}
}

// Cleanup 清理过期的 IP 状态条目。适合由调用方定期调用(如每 10 分钟)。
func (d *Defender) Cleanup() {
	now := time.Now()
	cutoff := now.Add(-d.cfg.connWindow - d.cfg.banDuration)

	d.mu.Lock()
	defer d.mu.Unlock()
	for ip, st := range d.ips {
		if now.After(st.bannedUtil) && len(st.conns) == 0 && len(st.flashes) == 0 {
			delete(d.ips, ip)
			continue
		}
		if now.After(st.bannedUtil) && lastBefore(st.conns, cutoff) && lastBefore(st.flashes, cutoff) {
			delete(d.ips, ip)
		}
	}
}

// Middleware 返回 HTTP 中间件,拦截被封禁 IP 的请求(返回 403)。
func (d *Defender) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := d.Admit(r.RemoteAddr); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (d *Defender) getOrCreate(ip string) *ipState {
	st, ok := d.ips[ip]
	if !ok {
		st = &ipState{}
		d.ips[ip] = st
	}
	return st
}

func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func appendAndTrim(times []time.Time, now time.Time, window time.Duration) []time.Time {
	cutoff := now.Add(-window)
	// 移除过期条目
	n := 0
	for _, t := range times {
		if t.After(cutoff) {
			times[n] = t
			n++
		}
	}
	times = times[:n]
	return append(times, now)
}

func lastBefore(times []time.Time, cutoff time.Time) bool {
	for _, t := range times {
		if t.After(cutoff) {
			return false
		}
	}
	return true
}
