// Package quictransport 提供基于 QUIC 的 p2p.Transport 实现。
//
// 适合场景:
//   - 服务端之间的高性能 P2P 通信(跨数据中心、边缘节点)
//   - 游戏服务器之间的状态同步(真正的可靠+不可靠双通道)
//   - 需要 0-RTT 快速重连的场景
//   - 移动端原生应用(连接迁移:WiFi↔4G 切换不断连)
//
// 特点:
//   - 可靠通道:QUIC Stream(多路复用、无跨流队头阻塞)
//   - 不可靠通道:QUIC Datagram(RFC 9221,不重传、不阻塞——真正的 UDP 语义)
//   - 内建 TLS 1.3(加密是强制的,无明文模式)
//   - 连接迁移(IP 变化不需要重新握手)
//   - 比 TCP 更好的 NAT 穿越能力(UDP 打洞比 TCP 容易)
//
// 与 tcptransport 的区别:
//   - TCP 的"不可靠通道"实际仍走 TCP(可靠有序),只是标记不同
//   - QUIC 的不可靠通道是真正的 datagram——丢了不重传、延迟最低
//
// 依赖:复用 pkg/quic 的连接管理能力。
package quictransport

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/rushteam/beauty/pkg/p2p"
	"github.com/rushteam/beauty/pkg/quic"

	quicgo "github.com/quic-go/quic-go"
)

// Transport 基于 QUIC 的 P2P 传输,同时支持可靠流和不可靠数据报。
type Transport struct {
	localID p2p.PeerID
	server  *quic.Server
	addrs   map[p2p.PeerID]string
	mu      sync.RWMutex
	accept  chan p2p.PeerConn
	closed  chan struct{}
	once    sync.Once
	dialOps []quic.DialOption
}

// Option 配置 Transport。
type Option func(*Transport)

// WithAddressBook 设置已知 peer 的地址映射。
func WithAddressBook(addrs map[p2p.PeerID]string) Option {
	return func(t *Transport) {
		for k, v := range addrs {
			t.addrs[k] = v
		}
	}
}

// WithDialOptions 设置客户端拨号选项(如 TLS 配置)。
func WithDialOptions(opts ...quic.DialOption) Option {
	return func(t *Transport) { t.dialOps = opts }
}

// New 创建 QUIC Transport。serverOpts 用于配置底层 QUIC Server(TLS 等)。
func New(localID p2p.PeerID, listenAddr string, serverOpts []quic.Option, opts ...Option) (*Transport, error) {
	t := &Transport{
		localID: localID,
		addrs:   make(map[p2p.PeerID]string),
		accept:  make(chan p2p.PeerConn, 16),
		closed:  make(chan struct{}),
	}
	for _, o := range opts {
		o(t)
	}

	srv := quic.NewServer(listenAddr, t.handleConn, serverOpts...)
	t.server = srv

	// 后台启动 server
	go func() {
		_ = srv.Start(context.Background())
	}()
	<-srv.Ready()

	return t, nil
}

func (t *Transport) handleConn(ctx context.Context, c *quic.Conn) error {
	// 握手:等待对端通过第一条流发送 peer ID
	stream, err := c.AcceptStream(ctx)
	if err != nil {
		return err
	}
	remotePeerID, err := readStreamHandshake(stream)
	if err != nil {
		return err
	}
	// 回复本机 ID
	if err := writeStreamHandshake(stream, t.localID); err != nil {
		return err
	}

	pc := newConn(remotePeerID, c, stream)

	select {
	case t.accept <- pc:
	case <-t.closed:
		pc.Close()
		return nil
	}

	// 阻塞直到连接关闭
	<-pc.Context().Done()
	return nil
}

// SetAddr 动态添加/更新 peer 地址。
func (t *Transport) SetAddr(peerID p2p.PeerID, addr string) {
	t.mu.Lock()
	t.addrs[peerID] = addr
	t.mu.Unlock()
}

// Addr 返回监听地址。如果监听了通配地址(如 ":0"),返回 localhost 可连接版本。
func (t *Transport) Addr() string {
	if addr := t.server.Addr(); addr != nil {
		host, port, _ := net.SplitHostPort(addr.String())
		if host == "" || host == "::" || host == "0.0.0.0" {
			return "127.0.0.1:" + port
		}
		return addr.String()
	}
	return ""
}

// Connect 主动连接目标 peer。
func (t *Transport) Connect(ctx context.Context, peerID p2p.PeerID) (p2p.PeerConn, error) {
	t.mu.RLock()
	addr, ok := t.addrs[peerID]
	t.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("quictransport: unknown peer %q", peerID)
	}

	conn, err := quic.Dial(ctx, addr, t.dialOps...)
	if err != nil {
		return nil, fmt.Errorf("quictransport: dial %s (%s): %w", peerID, addr, err)
	}

	// 握手:通过第一条流发送本机 ID
	stream, err := conn.OpenStream(ctx)
	if err != nil {
		conn.Close("handshake failed")
		return nil, fmt.Errorf("quictransport: open handshake stream: %w", err)
	}
	if err := writeStreamHandshake(stream, t.localID); err != nil {
		conn.Close("handshake failed")
		return nil, err
	}
	remotePeerID, err := readStreamHandshake(stream)
	if err != nil {
		conn.Close("handshake failed")
		return nil, err
	}
	if remotePeerID != peerID {
		conn.Close("peer ID mismatch")
		return nil, fmt.Errorf("quictransport: peer ID mismatch: expected %q, got %q", peerID, remotePeerID)
	}

	return newConn(peerID, conn, stream), nil
}

// Accept 阻塞等待入站连接。
func (t *Transport) Accept(ctx context.Context) (p2p.PeerConn, error) {
	select {
	case pc := <-t.accept:
		return pc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, fmt.Errorf("quictransport: closed")
	}
}

// Close 关闭 Transport。
func (t *Transport) Close() error {
	t.once.Do(func() {
		close(t.closed)
	})
	return nil
}

// ─── 握手(通过第一条 QUIC stream 交换 peer ID) ───

func writeStreamHandshake(s *quicgo.Stream, peerID p2p.PeerID) error {
	data := []byte(peerID)
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(data)))
	if _, err := s.Write(hdr); err != nil {
		return fmt.Errorf("quictransport: handshake write: %w", err)
	}
	if _, err := s.Write(data); err != nil {
		return fmt.Errorf("quictransport: handshake write: %w", err)
	}
	return nil
}

func readStreamHandshake(s *quicgo.Stream) (p2p.PeerID, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(s, hdr); err != nil {
		return "", fmt.Errorf("quictransport: handshake read: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr)
	if n > 1024 {
		return "", fmt.Errorf("quictransport: peer ID too long (%d)", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(s, data); err != nil {
		return "", fmt.Errorf("quictransport: handshake read: %w", err)
	}
	return p2p.PeerID(data), nil
}

// ─── quicConn 实现 p2p.PeerConn ───

type quicConn struct {
	peerID      p2p.PeerID
	conn        *quic.Conn
	ctrlStream  *quicgo.Stream // 握手时建立的控制流,也用于可靠消息
	recv        chan p2p.Message
	ctx         context.Context
	cancel      context.CancelFunc
	once        sync.Once
	reliableWMu sync.Mutex
}

func newConn(peerID p2p.PeerID, conn *quic.Conn, ctrlStream *quicgo.Stream) *quicConn {
	ctx, cancel := context.WithCancel(conn.Context())
	c := &quicConn{
		peerID:     peerID,
		conn:       conn,
		ctrlStream: ctrlStream,
		recv:       make(chan p2p.Message, 64),
		ctx:        ctx,
		cancel:     cancel,
	}
	go c.readReliableLoop()
	go c.readDatagramLoop()
	return c
}

func (c *quicConn) ID() p2p.PeerID           { return c.peerID }
func (c *quicConn) Recv() <-chan p2p.Message { return c.recv }
func (c *quicConn) Context() context.Context { return c.ctx }

// SendReliable 通过 QUIC stream 发送(可靠有序)。
func (c *quicConn) SendReliable(data []byte) error {
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)

	c.reliableWMu.Lock()
	_, err := c.ctrlStream.Write(frame)
	c.reliableWMu.Unlock()
	if err != nil {
		return fmt.Errorf("quictransport: send reliable: %w", err)
	}
	return nil
}

// SendUnreliable 通过 QUIC datagram 发送(不可靠,不重传)。
func (c *quicConn) SendUnreliable(data []byte) error {
	if err := c.conn.SendDatagram(data); err != nil {
		return fmt.Errorf("quictransport: send datagram: %w", err)
	}
	return nil
}

func (c *quicConn) Close() error {
	c.once.Do(func() {
		c.cancel()
		c.conn.Close("peer closed")
	})
	return nil
}

func (c *quicConn) readReliableLoop() {
	defer c.Close()
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(c.ctrlStream, hdr); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr)
		if n > 16<<20 {
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.ctrlStream, payload); err != nil {
			return
		}
		select {
		case c.recv <- p2p.Message{From: c.peerID, Data: payload, Reliable: true}:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *quicConn) readDatagramLoop() {
	for {
		data, err := c.conn.ReceiveDatagram(c.ctx)
		if err != nil {
			return
		}
		select {
		case c.recv <- p2p.Message{From: c.peerID, Data: data, Reliable: false}:
		case <-c.ctx.Done():
			return
		}
	}
}
