// Package tcptransport 提供基于 TCP 的 p2p.Transport 实现。
//
// 适合场景:
//   - Go 服务之间的内网/同机房 P2P 通信(无 NAT)
//   - 局域网联机(游戏 LAN party、IoT 设备互联)
//   - 需要穿透企业防火墙(TCP 443 几乎不会被拦)
//   - 对可靠性要求高、对延迟不那么敏感的场景
//
// 特点:
//   - 实现最简单,零外部依赖,只用 net 标准库
//   - 可靠通道:TCP 连接本身(有序、可靠)
//   - 不可靠通道:降级为 TCP 发送(仍可靠有序,语义不变但无真正的"可丢弃")
//   - 不支持 NAT 穿透——双方必须网络可达
//
// 如果需要真正的不可靠通道(丢包无重传),请使用 quictransport 或 webrtctransport。
package tcptransport

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/rushteam/beauty/pkg/transport/p2p"
)

const (
	flagReliable   byte = 0x01
	flagUnreliable byte = 0x02
)

// Transport 基于 TCP 的 P2P 传输。
type Transport struct {
	localID  p2p.PeerID
	listener net.Listener
	addrs    map[p2p.PeerID]string // peerID → "host:port"
	mu       sync.RWMutex
	closed   chan struct{}
	once     sync.Once
}

// Option 配置 Transport。
type Option func(*Transport)

// WithAddressBook 设置已知 peer 的地址映射。Connect 时根据 peerID 查找目标地址。
func WithAddressBook(addrs map[p2p.PeerID]string) Option {
	return func(t *Transport) {
		for k, v := range addrs {
			t.addrs[k] = v
		}
	}
}

// New 创建 TCP Transport。listenAddr 为本地监听地址(如 ":0" 由系统分配端口)。
func New(localID p2p.PeerID, listenAddr string, opts ...Option) (*Transport, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("tcptransport: listen %s: %w", listenAddr, err)
	}
	t := &Transport{
		localID:  localID,
		listener: ln,
		addrs:    make(map[p2p.PeerID]string),
		closed:   make(chan struct{}),
	}
	for _, o := range opts {
		o(t)
	}
	return t, nil
}

// SetAddr 动态添加/更新 peer 地址(运行时发现新 peer 后调用)。
func (t *Transport) SetAddr(peerID p2p.PeerID, addr string) {
	t.mu.Lock()
	t.addrs[peerID] = addr
	t.mu.Unlock()
}

// Addr 返回监听地址。
func (t *Transport) Addr() string {
	return t.listener.Addr().String()
}

// Connect 主动连接目标 peer。
func (t *Transport) Connect(ctx context.Context, peerID p2p.PeerID) (p2p.PeerConn, error) {
	t.mu.RLock()
	addr, ok := t.addrs[peerID]
	t.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tcptransport: unknown peer %q (not in address book)", peerID)
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcptransport: dial %s (%s): %w", peerID, addr, err)
	}

	// 握手:发送本机 ID
	if err := writeHandshake(conn, t.localID); err != nil {
		conn.Close()
		return nil, err
	}
	// 读取对端确认
	remotePeerID, err := readHandshake(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if remotePeerID != peerID {
		conn.Close()
		return nil, fmt.Errorf("tcptransport: peer ID mismatch: expected %q, got %q", peerID, remotePeerID)
	}

	return newConn(peerID, conn), nil
}

// Accept 阻塞等待入站连接。
func (t *Transport) Accept(ctx context.Context) (p2p.PeerConn, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.closed:
			return nil, net.ErrClosed
		default:
		}

		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.closed:
				return nil, net.ErrClosed
			default:
				return nil, fmt.Errorf("tcptransport: accept: %w", err)
			}
		}

		// 握手:读取对端 ID
		remotePeerID, err := readHandshake(conn)
		if err != nil {
			conn.Close()
			continue
		}
		// 回复本机 ID
		if err := writeHandshake(conn, t.localID); err != nil {
			conn.Close()
			continue
		}

		return newConn(remotePeerID, conn), nil
	}
}

// Close 关闭 Transport。
func (t *Transport) Close() error {
	t.once.Do(func() {
		close(t.closed)
		t.listener.Close()
	})
	return nil
}

// ─── 握手协议(4 字节长度 + peer ID) ───

func writeHandshake(conn net.Conn, peerID p2p.PeerID) error {
	data := []byte(peerID)
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(data)))
	if _, err := conn.Write(hdr); err != nil {
		return fmt.Errorf("tcptransport: handshake write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("tcptransport: handshake write id: %w", err)
	}
	return nil
}

func readHandshake(conn net.Conn) (p2p.PeerID, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return "", fmt.Errorf("tcptransport: handshake read length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr)
	if n > 1024 {
		return "", fmt.Errorf("tcptransport: handshake peer ID too long (%d)", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(conn, data); err != nil {
		return "", fmt.Errorf("tcptransport: handshake read id: %w", err)
	}
	return p2p.PeerID(data), nil
}

// ─── tcpConn 实现 p2p.PeerConn ───

type tcpConn struct {
	peerID p2p.PeerID
	conn   net.Conn
	recv   chan p2p.Message
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	wmu    sync.Mutex // 写锁,保证帧完整性
}

func newConn(peerID p2p.PeerID, raw net.Conn) *tcpConn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &tcpConn{
		peerID: peerID,
		conn:   raw,
		recv:   make(chan p2p.Message, 64),
		ctx:    ctx,
		cancel: cancel,
	}
	go c.readLoop()
	return c
}

func (c *tcpConn) ID() p2p.PeerID           { return c.peerID }
func (c *tcpConn) Recv() <-chan p2p.Message { return c.recv }
func (c *tcpConn) Context() context.Context { return c.ctx }

func (c *tcpConn) SendReliable(data []byte) error {
	return c.send(flagReliable, data)
}

func (c *tcpConn) SendUnreliable(data []byte) error {
	// TCP 没有真正的不可靠通道——降级为可靠发送
	return c.send(flagUnreliable, data)
}

func (c *tcpConn) Close() error {
	c.once.Do(func() {
		c.cancel()
		c.conn.Close()
	})
	return nil
}

// 帧格式: [1 byte flag][4 bytes length][payload]
func (c *tcpConn) send(flag byte, data []byte) error {
	frame := make([]byte, 5+len(data))
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
	copy(frame[5:], data)

	c.wmu.Lock()
	_, err := c.conn.Write(frame)
	c.wmu.Unlock()
	if err != nil {
		return fmt.Errorf("tcptransport: write: %w", err)
	}
	return nil
}

func (c *tcpConn) readLoop() {
	defer close(c.recv)
	defer c.Close()

	hdr := make([]byte, 5)
	for {
		if _, err := io.ReadFull(c.conn, hdr); err != nil {
			return
		}
		flag := hdr[0]
		n := binary.BigEndian.Uint32(hdr[1:5])
		if n > 16<<20 { // 16MB 上限
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.conn, payload); err != nil {
			return
		}

		msg := p2p.Message{
			From:     c.peerID,
			Data:     payload,
			Reliable: flag == flagReliable,
		}
		select {
		case c.recv <- msg:
		case <-c.ctx.Done():
			return
		}
	}
}
