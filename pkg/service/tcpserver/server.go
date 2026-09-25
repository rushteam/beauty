// Package tcpserver 提供原生 TCP 服务,作为 beauty.Service 运行。适合需要自定义二进制
// 协议的场景(IoT 设备接入、游戏网关、私有 RPC 等)。
//
// 设计:
//   - 每个连接由用户传入的 Handler 处理(handler 返回即连接关闭);
//   - 内置连接准入控制(MaxConns)、TLS、优雅关停(先停止 Accept,再等活跃连接排空);
//   - 空闲超时(WithReadTimeout/WithWriteTimeout):对每次 Read/Write 施加滚动 deadline,
//     长时间无收发的连接会在 handler 的读写处返回超时错误,从而被回收,避免连接泄漏;
//   - 连接预检与生命周期钩子(WithOnAccept/WithOnClose):接入后、交给 handler 前先做准入
//     (如扫描器过滤、心跳探活),关闭时回调清理。钩子只提供机制,准入策略由调用方注入;
//   - 满足 beauty.Service + ReadyNotifier + discover.Service。
//
// 用法:
//
//	srv := tcpserver.New(":9000", func(conn net.Conn) {
//	    defer conn.Close()
//	    // 读写 conn...
//	}, tcpserver.WithMaxConns(10000))
//	app := beauty.New(beauty.WithService(srv))
package tcpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"github.com/rushteam/beauty/pkg/utils/uuid"
)

var _ discover.Service = (*Server)(nil)

// Handler 处理一个 TCP 连接。函数返回后框架自动关闭连接(若 handler 未关闭)。
// ctx 在服务停止时取消——handler 应监听 ctx.Done() 以便优雅退出。
type Handler func(ctx context.Context, conn net.Conn)

// Option 配置 Server。
type Option func(*Server)

// WithServiceName 设置服务名(日志/注册中心标识)。
func WithServiceName(name string) Option {
	return func(s *Server) { s.name = name }
}

// WithMaxConns 设置最大并发连接数。超过上限时新连接立即关闭。<=0 表示不限(默认)。
func WithMaxConns(n int) Option {
	return func(s *Server) { s.maxConns = n }
}

// WithKeepAlive 设置 TCP KeepAlive 间隔(默认 15s;<=0 禁用)。
func WithKeepAlive(d time.Duration) Option {
	return func(s *Server) { s.keepAlive = d }
}

// WithTLS 通过证书文件启用 TLS。
func WithTLS(certFile, keyFile string) Option {
	return func(s *Server) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			panic(fmt.Sprintf("tcpserver: failed to load TLS key pair: %v", err))
		}
		if s.tlsConfig == nil {
			s.tlsConfig = &tls.Config{}
		}
		s.tlsConfig.Certificates = append(s.tlsConfig.Certificates, cert)
	}
}

// WithTLSConfig 通过自定义 tls.Config 启用 TLS。
func WithTLSConfig(cfg *tls.Config) Option {
	return func(s *Server) { s.tlsConfig = cfg }
}

// WithMetadata 设置注册中心元数据。
func WithMetadata(md map[string]string) Option {
	return func(s *Server) { maps.Copy(s.metadata, md) }
}

// WithShutdownTimeout 设置优雅关闭最长等待(默认 30s)。超时后强制关闭剩余连接。
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}

// WithReadTimeout 设置读空闲超时:每次 Read 前将 read deadline 顺延 d。
// 连接在 d 内没有可读数据时,handler 的 Read 会返回超时错误(net.Error.Timeout()),
// handler 据此退出即回收该连接,避免空闲/半开连接长期占用。<=0(默认)禁用。
func WithReadTimeout(d time.Duration) Option {
	return func(s *Server) { s.readTimeout = d }
}

// WithWriteTimeout 设置写超时:每次 Write 前将 write deadline 顺延 d,
// 防止对端不收数据导致的写阻塞长期挂起。<=0(默认)禁用。
func WithWriteTimeout(d time.Duration) Option {
	return func(s *Server) { s.writeTimeout = d }
}

// WithOnAccept 设置连接预检钩子:在连接被交给 handler 之前调用。
// 返回非 nil 错误则该连接被直接关闭、不进入 handler(用于扫描器过滤、心跳探活、
// 黑名单等准入控制)。本选项只提供机制,准入策略由调用方在 fn 中实现。例如接入
// pkg/transport/scandefender:
//
//	sd := scandefender.New()
//	tcpserver.WithOnAccept(func(_ context.Context, conn net.Conn) error {
//	    return sd.Admit(conn.RemoteAddr().String())
//	})
func WithOnAccept(fn func(ctx context.Context, conn net.Conn) error) Option {
	return func(s *Server) { s.onAccept = fn }
}

// WithOnClose 设置连接关闭回调:在每个连接结束(handler 返回或预检拒绝后)时调用,
// 用于释放与该连接相关的资源(如 scandefender.OnClose、在线表清理等)。
func WithOnClose(fn func(conn net.Conn)) Option {
	return func(s *Server) { s.onClose = fn }
}

// New 创建 TCP 服务。handler 处理每个接入的连接。
func New(listenAddr string, handler Handler, opts ...Option) *Server {
	s := &Server{
		id:              uuid.New(),
		name:            "tcp-server",
		metadata:        map[string]string{"kind": "tcp"},
		addr:            listenAddr,
		handler:         handler,
		ready:           make(chan struct{}),
		keepAlive:       15 * time.Second,
		shutdownTimeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Server 是原生 TCP 服务,满足 beauty.Service / ReadyNotifier / discover.Service。
type Server struct {
	id       string
	name     string
	metadata map[string]string

	addr            string
	handler         Handler
	ready           chan struct{}
	maxConns        int
	keepAlive       time.Duration
	tlsConfig       *tls.Config
	shutdownTimeout time.Duration
	readTimeout     time.Duration
	writeTimeout    time.Duration
	onAccept        func(ctx context.Context, conn net.Conn) error
	onClose         func(conn net.Conn)

	activeConns atomic.Int64
	connWg      sync.WaitGroup
}

// Start 监听并服务,ctx 取消时优雅关停。满足 beauty.Service。
func (s *Server) Start(ctx context.Context) error {
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(s.ready) }) }
	defer signalReady()

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()

	if s.tlsConfig != nil {
		ln = tls.NewListener(ln, s.tlsConfig)
	}

	signalReady()
	logger.Info("tcp server serve", "addr", s.addr)

	// accept 循环
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Debug("tcp server accept error", "err", err)
				continue
			}
			if s.maxConns > 0 && s.activeConns.Load() >= int64(s.maxConns) {
				_ = conn.Close()
				continue
			}
			if tc, ok := conn.(*net.TCPConn); ok && s.keepAlive > 0 {
				_ = tc.SetKeepAlive(true)
				_ = tc.SetKeepAlivePeriod(s.keepAlive)
			}
			s.activeConns.Add(1)
			s.connWg.Add(1)
			go s.serve(ctx, conn)
		}
	}()

	<-ctx.Done()
	logger.Info("tcp server stopping...")

	// 停止 Accept
	_ = ln.Close()

	// 等待活跃连接排空
	done := make(chan struct{})
	go func() {
		s.connWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(s.shutdownTimeout):
		logger.Warn("tcp server shutdown timeout, connections may be forcefully closed",
			"active", s.activeConns.Load(), "timeout", s.shutdownTimeout)
	}
	logger.Info("tcp server stopped")
	return nil
}

func (s *Server) serve(ctx context.Context, conn net.Conn) {
	defer func() {
		if s.onClose != nil {
			s.onClose(conn)
		}
		_ = conn.Close()
		s.activeConns.Add(-1)
		s.connWg.Done()
	}()

	// 连接预检:准入失败直接关闭,不进入 handler。
	if s.onAccept != nil {
		if err := s.onAccept(ctx, conn); err != nil {
			logger.Debug("tcp server connection rejected", "remote", conn.RemoteAddr().String(), "err", err)
			return
		}
	}

	// 按需包装读写超时,使空闲连接可被回收。
	c := conn
	if s.readTimeout > 0 || s.writeTimeout > 0 {
		c = &timeoutConn{Conn: conn, readTimeout: s.readTimeout, writeTimeout: s.writeTimeout}
	}
	s.handler(ctx, c)
}

// timeoutConn 在每次 Read/Write 前顺延对应的 deadline,实现"空闲超时"语义。
type timeoutConn struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
}

func (c *timeoutConn) Read(b []byte) (int, error) {
	if c.readTimeout > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	return c.Conn.Read(b)
}

func (c *timeoutConn) Write(b []byte) (int, error) {
	if c.writeTimeout > 0 {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	return c.Conn.Write(b)
}

// Ready 在端口监听成功后关闭。满足 beauty.ReadyNotifier。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// String 满足 beauty.Service。
func (s *Server) String() string { return addr.ParseHostPort(s.addr) }

// ActiveConns 返回当前活跃连接数。
func (s *Server) ActiveConns() int64 { return s.activeConns.Load() }

// --- discover.Service ---

func (s *Server) ID() string                  { return s.id }
func (s *Server) Name() string                { return s.name }
func (s *Server) Kind() string                { return "tcp" }
func (s *Server) Addr() string                { return addr.ParseHostPort(s.addr) }
func (s *Server) Metadata() map[string]string { return s.metadata }
