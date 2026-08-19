// Package quic 提供基于 QUIC(quic-go)的连接层,作为 pkg/ws(WebSocket/TCP)之外
// 面向实时/游戏同步的可选传输。opt-in 子包,按需 import,不污染现有 TCP 栈。
//
// 为什么是 QUIC:一条连接上同时给你两种通道,正好覆盖游戏同步的两类数据——
//   - 可靠有序流(OpenStream/AcceptStream):关键指令、登录、聊天,像 TCP 但多路
//     复用、跨流无队头阻塞(HoL);
//   - 不可靠数据报(SendDatagram/ReceiveDatagram,RFC 9221):高频状态/位置更新,
//     丢了就丢、不重传、不阻塞后续——正是状态同步想要的语义(TCP 给不了)。
//
// 另外 QUIC 内建 TLS 1.3(加密是强制的)、连接迁移、0-RTT。
//
// 语义与边界:
//   - TLS 必填。生产传 WithTLSConfig;没传时 Server 自动生成自签证书并告警(仅供
//     开发/示例,客户端需 WithInsecureSkipVerify)。见 DevTLSConfig。
//   - 数据报受 MTU 限制(通常 ≲1200 字节),大负载走可靠流或自行分片。
//   - Server 结构上满足 beauty.Service(Start/String)+ ReadyNotifier,可直接
//     beauty.WithService(srv) 挂进框架、随 app 优雅停机。
//
// 本包只做「薄传输」:Conn 是对 quic-go 连接的封装,流/数据报直接透出,不叠加
// 应用协议(序列化、房间、AOI 等由上层如 pkg/gameloop + 你的编解码决定)。
//
// 性能(生产要点):
//   - UDP 缓冲区:quic-go 期望内核 UDP 收发缓冲各 7MB;OS 常按 net.core.rmem_max /
//     wmem_max 压低上限,高负载下会丢包。生产请先放开(Linux:
//     `sysctl -w net.core.rmem_max=7500000 net.core.wmem_max=7500000`),
//     并用 ListenUDP 建 socket(已尽力把缓冲提到 7MB)交给 WithPacketConn。
//   - Socket 复用:自备 quic.Transport(WithTransport / WithDialTransport)可让一条
//     UDP socket 同时承载「服务端监听 + 多个客户端拨号」(靠连接 ID 解复用),省 fd/内存,
//     适合既收玩家又拨对端的网关。
//   - GSO 批量发送在 Linux 上由 quic-go 自动开启,无需配置。
package quic

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	quicgo "github.com/quic-go/quic-go"
)

// defaultALPN 是握手协商的应用层协议名,server 与 client 必须一致。
const defaultALPN = "beauty-quic"

// desiredUDPBuffer 是 quic-go 期望的内核 UDP 收发缓冲区大小(7MB)。
const desiredUDPBuffer = 7 << 20

// ListenUDP 建一个 UDP socket 并尽力把收发缓冲区提到 7MB(减少高负载丢包)。OS 可能
// 仍按 net.core.rmem_max / wmem_max 压低(见包注释的 sysctl 提示)。返回的 socket 可
// 直接交给 WithPacketConn,或包进 quic.Transport 在多个 Server/Dial 间复用。
func ListenUDP(addr string) (*net.UDPConn, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("quic: resolve udp %s: %w", addr, err)
	}
	pc, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, fmt.Errorf("quic: listen udp %s: %w", addr, err)
	}
	_ = pc.SetReadBuffer(desiredUDPBuffer)  // best-effort;OS 可能压低上限
	_ = pc.SetWriteBuffer(desiredUDPBuffer) // best-effort
	return pc, nil
}

// Conn 是一条 QUIC 连接的薄封装:既能开可靠流,也能收发不可靠数据报。
type Conn struct {
	raw *quicgo.Conn
}

// SendDatagram 发送一条不可靠数据报(不重传、不保证到达/顺序)。适合高频状态更新。
// 负载过大(超过路径 MTU)会返回错误——大负载请走可靠流。
func (c *Conn) SendDatagram(b []byte) error {
	if err := c.raw.SendDatagram(b); err != nil {
		return fmt.Errorf("quic: send datagram: %w", err)
	}
	return nil
}

// ReceiveDatagram 阻塞接收下一条数据报,直到收到或 ctx 取消。
func (c *Conn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	b, err := c.raw.ReceiveDatagram(ctx)
	if err != nil {
		return nil, fmt.Errorf("quic: receive datagram: %w", err)
	}
	return b, nil
}

// OpenStream 打开一条双向可靠有序流(阻塞直到流控允许或 ctx 取消)。
// 返回的 *quicgo.Stream 是 io.ReadWriteCloser:Close 关闭发送方向,仍可继续读。
func (c *Conn) OpenStream(ctx context.Context) (*quicgo.Stream, error) {
	s, err := c.raw.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("quic: open stream: %w", err)
	}
	return s, nil
}

// AcceptStream 阻塞接收对端打开的下一条流,直到到达或 ctx 取消。
func (c *Conn) AcceptStream(ctx context.Context) (*quicgo.Stream, error) {
	s, err := c.raw.AcceptStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("quic: accept stream: %w", err)
	}
	return s, nil
}

// Context 返回连接的生命周期 ctx(连接关闭时取消)。
func (c *Conn) Context() context.Context { return c.raw.Context() }

// RemoteAddr 返回对端地址。
func (c *Conn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }

// LocalAddr 返回本地地址。
func (c *Conn) LocalAddr() net.Addr { return c.raw.LocalAddr() }

// Raw 返回底层 quic-go 连接,供需要高级能力时使用。
func (c *Conn) Raw() *quicgo.Conn { return c.raw }

// Close 关闭连接(附带 reason,对端可读)。幂等。
func (c *Conn) Close(reason string) error {
	return c.raw.CloseWithError(0, reason)
}

func defaultQUICConfig() *quicgo.Config {
	return &quicgo.Config{
		EnableDatagrams: true, // 打开不可靠数据报通道
		MaxIdleTimeout:  30 * time.Second,
		KeepAlivePeriod: 15 * time.Second,
	}
}

// Handler 处理一条被接受的连接。ctx 在 server 停机时取消——handler 应据此退出。
type Handler func(ctx context.Context, c *Conn) error

// Server 是一个 QUIC 服务端,结构上满足 beauty.Service + ReadyNotifier。
// 零值不可用,用 NewServer 构造。
type Server struct {
	addr    string
	handler Handler
	name    string
	tls     *tls.Config
	qconf   *quicgo.Config

	pconn     net.PacketConn    // WithPacketConn:自备(已调好缓冲的)UDP socket
	transport *quicgo.Transport // WithTransport:自备可复用的 transport(优先于 pconn)
	ln        *quicgo.Listener
	ready     chan struct{}
	readyOnce sync.Once
}

// Option 配置 Server。
type Option func(*Server)

// WithTLSConfig 设置服务端 TLS(生产必填;须包含证书,NextProtos 会在缺省时补上默认 ALPN)。
func WithTLSConfig(c *tls.Config) Option { return func(s *Server) { s.tls = c } }

// WithQUICConfig 覆盖 quic.Config(默认开启数据报、30s idle、15s keepalive)。
func WithQUICConfig(c *quicgo.Config) Option {
	return func(s *Server) {
		if c != nil {
			s.qconf = c
		}
	}
}

// WithServiceName 设置服务名(日志/标识用)。
func WithServiceName(name string) Option {
	return func(s *Server) {
		if name != "" {
			s.name = name
		}
	}
}

// WithPacketConn 让 Server 在自备的 UDP socket 上监听(而非按 addr 新建),便于用
// ListenUDP 预调缓冲区。socket 生命周期由调用方管理(Server 停机只关 Listener,不关 socket)。
func WithPacketConn(pc net.PacketConn) Option { return func(s *Server) { s.pconn = pc } }

// WithTransport 让 Server 复用一个自备的 quic.Transport(优先于 WithPacketConn),
// 可与客户端 Dial 共享同一条 UDP socket。transport 生命周期由调用方管理。
func WithTransport(t *quicgo.Transport) Option { return func(s *Server) { s.transport = t } }

// NewServer 创建 QUIC 服务端。addr 形如 "127.0.0.1:8443"(":0" 由系统选端口,
// 之后可用 Addr() 取回)。handler 处理每条连接。
func NewServer(addr string, handler Handler, opts ...Option) *Server {
	s := &Server{
		addr:    addr,
		handler: handler,
		name:    "quic",
		qconf:   defaultQUICConfig(),
		ready:   make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Start 监听并循环接受连接,每条连接起一个 goroutine 跑 handler;ctx 取消时停止
// 接受、关闭在途连接并等其退出——满足 beauty.Service。
func (s *Server) Start(ctx context.Context) error {
	tlsConf := s.tls
	if tlsConf == nil {
		dev, err := DevTLSConfig()
		if err != nil {
			return fmt.Errorf("quic: generate dev tls: %w", err)
		}
		tlsConf = dev
		slog.Warn("quic: no TLS config set, using generated self-signed cert (DEV ONLY)", "service", s.name)
	} else if len(tlsConf.NextProtos) == 0 {
		tlsConf = tlsConf.Clone()
		tlsConf.NextProtos = []string{defaultALPN}
	}

	var (
		ln  *quicgo.Listener
		err error
	)
	switch {
	case s.transport != nil: // 自备 transport(可与客户端共享 socket)
		ln, err = s.transport.Listen(tlsConf, s.qconf)
	case s.pconn != nil: // 自备 socket(如 ListenUDP 调好缓冲的)
		s.transport = &quicgo.Transport{Conn: s.pconn}
		ln, err = s.transport.Listen(tlsConf, s.qconf)
	default: // 按 addr 新建(quic-go 内部持有 socket)
		ln, err = quicgo.ListenAddr(s.addr, tlsConf, s.qconf)
	}
	if err != nil {
		return fmt.Errorf("quic: listen %s: %w", s.addr, err)
	}
	s.ln = ln
	s.readyOnce.Do(func() { close(s.ready) })

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			break // ctx 取消或 listener 关闭
		}
		wg.Add(1)
		go s.serve(ctx, conn, &wg)
	}
	_ = ln.Close()
	wg.Wait()
	return nil
}

func (s *Server) serve(ctx context.Context, conn *quicgo.Conn, wg *sync.WaitGroup) {
	defer wg.Done()
	c := &Conn{raw: conn}

	// server 停机时关闭连接,解除 handler 在 Receive/Accept 上的阻塞。
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close("server shutdown")
		case <-stop:
		}
	}()

	if err := s.handler(ctx, c); err != nil && ctx.Err() == nil {
		slog.Debug("quic: handler returned error", "service", s.name, "err", err)
	}
	_ = c.Close("done")
}

// Ready 在开始监听后关闭——满足 beauty.ReadyNotifier。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr 返回实际监听地址(":0" 时用来取回系统分配的端口);未启动返回 nil。
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// String 满足 beauty.Service。
func (s *Server) String() string { return "quic.Server(" + s.name + "@" + s.addr + ")" }

// ===== 客户端 =====

type dialConfig struct {
	tls       *tls.Config
	qconf     *quicgo.Config
	insecure  bool
	transport *quicgo.Transport
}

// DialOption 配置 Dial。
type DialOption func(*dialConfig)

// WithClientTLSConfig 设置客户端 TLS(缺省时用一个只设 ALPN 的空配置)。
func WithClientTLSConfig(c *tls.Config) DialOption { return func(d *dialConfig) { d.tls = c } }

// WithClientQUICConfig 覆盖客户端 quic.Config。
func WithClientQUICConfig(c *quicgo.Config) DialOption {
	return func(d *dialConfig) {
		if c != nil {
			d.qconf = c
		}
	}
}

// WithInsecureSkipVerify 跳过服务端证书校验(仅供开发/自签证书场景)。
func WithInsecureSkipVerify(v bool) DialOption { return func(d *dialConfig) { d.insecure = v } }

// WithDialTransport 复用一个自备的 quic.Transport 拨号,可与 Server 共享同一条 UDP socket。
func WithDialTransport(t *quicgo.Transport) DialOption {
	return func(d *dialConfig) { d.transport = t }
}

// Dial 连接一个 QUIC 服务端。
func Dial(ctx context.Context, addr string, opts ...DialOption) (*Conn, error) {
	d := dialConfig{qconf: defaultQUICConfig()}
	for _, o := range opts {
		o(&d)
	}
	tlsConf := d.tls
	if tlsConf == nil {
		tlsConf = &tls.Config{}
	} else {
		tlsConf = tlsConf.Clone()
	}
	if len(tlsConf.NextProtos) == 0 {
		tlsConf.NextProtos = []string{defaultALPN}
	}
	if d.insecure {
		tlsConf.InsecureSkipVerify = true
	}
	var (
		raw *quicgo.Conn
		err error
	)
	if d.transport != nil { // 复用共享 socket 拨号(Transport.Dial 收 net.Addr)
		ua, rerr := net.ResolveUDPAddr("udp", addr)
		if rerr != nil {
			return nil, fmt.Errorf("quic: resolve %s: %w", addr, rerr)
		}
		raw, err = d.transport.Dial(ctx, ua, tlsConf, d.qconf)
	} else {
		raw, err = quicgo.DialAddr(ctx, addr, tlsConf, d.qconf)
	}
	if err != nil {
		return nil, fmt.Errorf("quic: dial %s: %w", addr, err)
	}
	return &Conn{raw: raw}, nil
}
