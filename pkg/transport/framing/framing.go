// Package framing 提供基于"长度前缀"的消息分帧编解码,解决 TCP 流的粘包/半包问题。
//
// 裸 TCP 是字节流,没有消息边界:一次 Read 可能读到半条消息(半包),也可能一次读到
// 多条消息(粘包)。最通用的解法是每条消息前加一个定长的长度头。本包提供该机制,
// 只负责"按长度切分字节",不关心 payload 的内容与序列化方式(那属于业务/协议策略)。
//
// 帧格式:
//
//	[N 字节大端长度][payload...]        // N ∈ {1,2,4},默认 4
//
// 特性:
//   - 长度头 1/2/4 字节可选,大/小端可选;
//   - MaxFrameSize 上限防御恶意超大长度(尤其 4 字节头);
//   - ReadFrame 用 io.ReadFull 读满,天然处理半包;循环 ReadFrame 天然处理粘包;
//   - Writer 写操作加锁,可被多个 goroutine 安全并发调用。
//
// 用法(配合 tcpserver):
//
//	codec := framing.New(framing.WithHeaderSize(2), framing.WithMaxFrameSize(64<<10))
//	srv := tcpserver.New(":9000", func(ctx context.Context, conn net.Conn) {
//	    r := codec.NewReader(conn)
//	    w := codec.NewWriter(conn)
//	    for {
//	        msg, err := r.ReadFrame()
//	        if err != nil { return } // EOF/超时/错误即退出
//	        // 解析 msg(protobuf/json/自定义) ...
//	        _ = w.WriteFrame(reply)
//	    }
//	})
package framing

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// 默认参数。
const (
	defaultHeaderSize   = 4
	defaultMaxFrameSize = 16 << 20 // 16 MiB
)

// ErrFrameTooLarge 表示待写入的 payload 或收到的长度头超过 MaxFrameSize / 长度头容量。
var ErrFrameTooLarge = errors.New("framing: frame too large")

// Codec 是可复用、并发安全的分帧编解码器。零值不可用,请用 New 创建。
type Codec struct {
	headerSize int
	maxFrame   int
	order      binary.ByteOrder
}

// Option 配置 Codec。
type Option func(*Codec)

// WithHeaderSize 设置长度头字节数,仅支持 1、2、4(默认 4)。
// 对应最大 payload 分别为 255 B、64 KiB-1、4 GiB-1(还会受 MaxFrameSize 约束)。
// 非法值将回退为默认 4。
func WithHeaderSize(n int) Option {
	return func(c *Codec) {
		if n == 1 || n == 2 || n == 4 {
			c.headerSize = n
		}
	}
}

// WithMaxFrameSize 设置单帧 payload 字节上限(默认 16 MiB)。
// 用于防御恶意/异常的超大长度头。<=0 表示仅受长度头容量约束。
func WithMaxFrameSize(n int) Option {
	return func(c *Codec) { c.maxFrame = n }
}

// WithLittleEndian 使用小端长度头(默认大端)。
func WithLittleEndian() Option {
	return func(c *Codec) { c.order = binary.LittleEndian }
}

// New 创建 Codec。
func New(opts ...Option) *Codec {
	c := &Codec{
		headerSize: defaultHeaderSize,
		maxFrame:   defaultMaxFrameSize,
		order:      binary.BigEndian,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// headerCap 返回长度头能表示的最大值。
func (c *Codec) headerCap() int {
	switch c.headerSize {
	case 1:
		return 1<<8 - 1
	case 2:
		return 1<<16 - 1
	default:
		// 4 字节:上限为 int 可表示范围内的 maxFrame,不直接用 1<<32-1 以免 32 位平台溢出。
		return 1<<31 - 1
	}
}

// limit 返回实际生效的 payload 上限(长度头容量与 MaxFrameSize 取小)。
func (c *Codec) limit() int {
	lim := c.headerCap()
	if c.maxFrame > 0 && c.maxFrame < lim {
		lim = c.maxFrame
	}
	return lim
}

// putHeader 把长度写入 hdr(长度为 c.headerSize)。
func (c *Codec) putHeader(hdr []byte, n int) {
	switch c.headerSize {
	case 1:
		hdr[0] = byte(n)
	case 2:
		c.order.PutUint16(hdr, uint16(n))
	default:
		c.order.PutUint32(hdr, uint32(n))
	}
}

// readHeader 从 hdr 解析长度。
func (c *Codec) readHeader(hdr []byte) int {
	switch c.headerSize {
	case 1:
		return int(hdr[0])
	case 2:
		return int(c.order.Uint16(hdr))
	default:
		return int(c.order.Uint32(hdr))
	}
}

// WriteFrame 把 payload 加上长度头写入 w。payload 超限返回 ErrFrameTooLarge。
// 注意:并发写同一个 w 需自行加锁,或改用 NewWriter。
func (c *Codec) WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > c.limit() {
		return fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, len(payload), c.limit())
	}
	// 单次写出"头+体",减少小包场景的写系统调用与 TCP 分片。
	buf := make([]byte, c.headerSize+len(payload))
	c.putHeader(buf[:c.headerSize], len(payload))
	copy(buf[c.headerSize:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame 从 r 读取一帧 payload。
//   - 数据不足会阻塞直到读满(半包由 io.ReadFull 处理);
//   - 连接干净关闭返回 io.EOF;读到一半关闭返回 io.ErrUnexpectedEOF;
//   - 长度头超过上限返回 ErrFrameTooLarge。
//
// 返回的切片为本次调用新分配,调用方可安全持有。
func (c *Codec) ReadFrame(r io.Reader) ([]byte, error) {
	hdr := make([]byte, c.headerSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err // io.EOF / io.ErrUnexpectedEOF / 其他
	}
	n := c.readHeader(hdr)
	if n > c.limit() {
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, n, c.limit())
	}
	if n == 0 {
		return []byte{}, nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return payload, nil
}

// Reader 在 io.Reader 上带缓冲地循环读帧,减少读系统调用。非并发安全。
type Reader struct {
	c  *Codec
	br *bufio.Reader
}

// NewReader 基于 r 创建带缓冲的 Reader。
func (c *Codec) NewReader(r io.Reader) *Reader {
	return &Reader{c: c, br: bufio.NewReader(r)}
}

// ReadFrame 读取下一帧,语义同 Codec.ReadFrame。
func (rd *Reader) ReadFrame() ([]byte, error) { return rd.c.ReadFrame(rd.br) }

// Writer 在 io.Writer 上写帧,写操作加锁,可被多个 goroutine 并发调用。
type Writer struct {
	c  *Codec
	w  io.Writer
	mu sync.Mutex
}

// NewWriter 基于 w 创建并发安全的 Writer。
func (c *Codec) NewWriter(w io.Writer) *Writer {
	return &Writer{c: c, w: w}
}

// WriteFrame 写出一帧,语义同 Codec.WriteFrame,但对并发写入加锁保护。
func (wr *Writer) WriteFrame(payload []byte) error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	return wr.c.WriteFrame(wr.w, payload)
}
