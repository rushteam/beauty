// Package rtmp 提供一个 RTMP 采集(ingest)服务端,薄封装 github.com/yutopp/go-rtmp。
// 接受推流端(OBS / ffmpeg 等)的 publish,把每一路流的 metadata / audio / video 数据
// 交给业务实现的 Handler。
//
// 边界(和框架"薄机制"一致):本包**只负责收流**——不转码、不封装 HLS。收到的
// audio/video 是 FLV tag body(含各自的编解码头),要落地成 HLS 分片需再接一层 remux
// (交给上层/ffmpeg,配合 pkg/hls 分发)。不重新发明 RTMP 协议,直接用成熟的纯 Go 实现。
//
// Server 结构上满足 beauty.Service(Start/String)+ ReadyNotifier,可直接
// beauty.WithService(srv) 挂进框架、随 app 优雅停机。默认监听 1935(RTMP 标准端口)。
//
// 用法:
//
//	srv := rtmp.NewServer(":1935", func(streamKey string) rtmp.Handler {
//	    // 按流名(推流地址 /app/<streamKey>)决定怎么处理,返回 nil 拒绝
//	    return &myHandler{key: streamKey}
//	})
//	app := beauty.New(beauty.WithService(srv))
package rtmp

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
	gortmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// Handler 处理一路已开始的推流。方法在该连接的读 goroutine 内串行调用。
type Handler interface {
	// OnMetaData 收到 onMetaData(如分辨率/码率/编解码信息)。payload 是原始 AMF 字节。可能不被调用。
	OnMetaData(payload []byte)
	// OnAudio 收到一帧音频(FLV audio tag body:首字节为音频编解码头,之后为数据)。
	OnAudio(timestamp uint32, data []byte) error
	// OnVideo 收到一帧视频(FLV video tag body:首字节含帧类型/编解码,之后为数据)。
	OnVideo(timestamp uint32, data []byte) error
	// OnClose 推流结束(任何原因)时调用一次,用于收尾。
	OnClose()
}

// PublishFunc 在收到一路 publish 时被调用(streamKey 取自推流地址的流名,可含 ?token=…),
// 返回处理该路流的 Handler;返回 nil 表示拒绝这次推流。这是**推流级鉴权**点——按流名/
// token 校验后决定接不接。
type PublishFunc func(streamKey string) Handler

// ConnectInfo 是 RTMP connect 命令携带的信息,用于连接级鉴权。
type ConnectInfo struct {
	App   string // 推流地址里的应用名,如 rtmp://host/<app>/<streamKey> 的 <app>
	TCURL string // 完整 tcUrl,常含鉴权 query(如 ...?sign=xxx)
}

// ConnectAuthFunc 在 RTMP connect 阶段被调用(早于 publish)。返回非 nil error 则拒绝
// 连接——用于在建连时就按 app/tcUrl(签名、来源)做**连接级鉴权**,把非法推流挡在更早。
type ConnectAuthFunc func(*ConnectInfo) error

// Server 是 RTMP 采集服务端。零值不可用,用 NewServer 构造。
type Server struct {
	addr        string
	onPublish   PublishFunc
	connectAuth ConnectAuthFunc
	name        string
	logger      logrus.FieldLogger

	ln        net.Listener
	srv       *gortmp.Server
	ready     chan struct{}
	readyOnce sync.Once
}

// Option 配置 Server。
type Option func(*Server)

// WithServiceName 设置服务名(日志/标识用)。
func WithServiceName(name string) Option {
	return func(s *Server) {
		if name != "" {
			s.name = name
		}
	}
}

// WithLogger 设置底层 go-rtmp 的日志器(默认丢弃,避免刷屏)。
func WithLogger(l logrus.FieldLogger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithConnectAuth 设置连接级鉴权:在 RTMP connect 阶段按 app/tcUrl 校验,返回 error 拒绝连接。
func WithConnectAuth(fn ConnectAuthFunc) Option {
	return func(s *Server) { s.connectAuth = fn }
}

// NewServer 创建 RTMP 采集服务端。addr 形如 ":1935"。onPublish 决定如何处理每一路推流。
func NewServer(addr string, onPublish PublishFunc, opts ...Option) *Server {
	discard := logrus.New()
	discard.SetOutput(io.Discard)
	s := &Server{
		addr:      addr,
		onPublish: onPublish,
		name:      "rtmp",
		logger:    discard,
		ready:     make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Start 监听并处理 RTMP 连接,直到 ctx 取消——满足 beauty.Service。
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("rtmp: listen %s: %w", s.addr, err)
	}
	s.ln = ln
	s.srv = gortmp.NewServer(&gortmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *gortmp.ConnConfig) {
			return conn, &gortmp.ConnConfig{
				Handler: &connHandler{onPublish: s.onPublish, connectAuth: s.connectAuth},
				Logger:  s.logger,
			}
		},
	})
	s.readyOnce.Do(func() { close(s.ready) })

	// ctx 取消时关闭 server(解除 Serve 的 Accept 阻塞)。
	go func() {
		<-ctx.Done()
		_ = s.srv.Close()
	}()

	err = s.srv.Serve(ln)
	if ctx.Err() != nil {
		return nil // 停机导致的返回,视为正常
	}
	return err
}

// Ready 在开始监听后关闭——满足 beauty.ReadyNotifier。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr 返回实际监听地址(":0" 时取回系统分配端口);未启动返回 nil。
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// String 满足 beauty.Service。
func (s *Server) String() string { return "rtmp.Server(" + s.name + "@" + s.addr + ")" }

// connHandler 把 go-rtmp 的连接回调桥接到业务 Handler。一条连接一个实例。
type connHandler struct {
	gortmp.DefaultHandler
	onPublish   PublishFunc
	connectAuth ConnectAuthFunc
	h           Handler
}

func (c *connHandler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	if c.connectAuth != nil {
		return c.connectAuth(&ConnectInfo{App: cmd.Command.App, TCURL: cmd.Command.TCURL})
	}
	return nil
}

func (c *connHandler) OnPublish(_ *gortmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	c.h = c.onPublish(cmd.PublishingName)
	if c.h == nil {
		return fmt.Errorf("rtmp: publish rejected: %s", cmd.PublishingName)
	}
	return nil
}

func (c *connHandler) OnSetDataFrame(_ uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if c.h != nil {
		c.h.OnMetaData(data.Payload)
	}
	return nil
}

func (c *connHandler) OnAudio(timestamp uint32, payload io.Reader) error {
	b, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	if c.h != nil {
		return c.h.OnAudio(timestamp, b)
	}
	return nil
}

func (c *connHandler) OnVideo(timestamp uint32, payload io.Reader) error {
	b, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	if c.h != nil {
		return c.h.OnVideo(timestamp, b)
	}
	return nil
}

func (c *connHandler) OnClose() {
	if c.h != nil {
		c.h.OnClose()
	}
}
