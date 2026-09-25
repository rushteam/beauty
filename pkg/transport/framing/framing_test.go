package framing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestRoundTripHeaderSizes(t *testing.T) {
	for _, hs := range []int{1, 2, 4} {
		hs := hs
		t.Run("header"+string(rune('0'+hs)), func(t *testing.T) {
			c := New(WithHeaderSize(hs))
			var buf bytes.Buffer
			msgs := [][]byte{[]byte("hello"), {}, []byte("world!!!"), []byte("a")}
			for _, m := range msgs {
				if err := c.WriteFrame(&buf, m); err != nil {
					t.Fatalf("WriteFrame: %v", err)
				}
			}
			r := c.NewReader(&buf)
			for i, want := range msgs {
				got, err := r.ReadFrame()
				if err != nil {
					t.Fatalf("ReadFrame #%d: %v", i, err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("frame #%d = %q, want %q", i, got, want)
				}
			}
			if _, err := r.ReadFrame(); err != io.EOF {
				t.Errorf("expected io.EOF at end, got %v", err)
			}
		})
	}
}

// TestStickyAndHalfPackets 验证粘包(一次给多帧)与半包(逐字节喂入)都能正确切分。
func TestStickyAndHalfPackets(t *testing.T) {
	c := New(WithHeaderSize(2))
	var buf bytes.Buffer
	want := [][]byte{[]byte("frame-one"), []byte("f2"), []byte("the-third-frame")}
	for _, m := range want {
		_ = c.WriteFrame(&buf, m)
	}
	raw := buf.Bytes()

	// 半包:用每次只吐 1 字节的 reader 喂入。
	r := c.NewReader(&oneByteReader{data: raw})
	for i, w := range want {
		got, err := r.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if !bytes.Equal(got, w) {
			t.Errorf("frame #%d = %q, want %q", i, got, w)
		}
	}
}

type oneByteReader struct {
	data []byte
	pos  int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

func TestWriteFrameTooLarge(t *testing.T) {
	c := New(WithHeaderSize(1)) // 上限 255
	err := c.WriteFrame(io.Discard, make([]byte, 256))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("expected ErrFrameTooLarge, got %v", err)
	}
	// MaxFrameSize 约束优先于长度头容量。
	c2 := New(WithHeaderSize(4), WithMaxFrameSize(10))
	if err := c2.WriteFrame(io.Discard, make([]byte, 11)); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("expected ErrFrameTooLarge from maxFrame, got %v", err)
	}
}

func TestReadFrameTooLargeGuardsMaliciousHeader(t *testing.T) {
	// 构造一个声称长度为 1<<20 但实际没有数据的 4 字节头,配合小 maxFrame 应被拒绝。
	c := New(WithHeaderSize(4), WithMaxFrameSize(1024))
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, 1<<20)
	_, err := c.ReadFrame(bytes.NewReader(hdr))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestReadFrameUnexpectedEOF(t *testing.T) {
	c := New(WithHeaderSize(2))
	// 头声称 5 字节,但只给 2 字节 body。
	var buf bytes.Buffer
	hdr := make([]byte, 2)
	binary.BigEndian.PutUint16(hdr, 5)
	buf.Write(hdr)
	buf.WriteString("ab")
	if _, err := c.ReadFrame(&buf); err != io.ErrUnexpectedEOF {
		t.Errorf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestLittleEndian(t *testing.T) {
	c := New(WithHeaderSize(2), WithLittleEndian())
	var buf bytes.Buffer
	_ = c.WriteFrame(&buf, []byte("xy"))
	// 小端下长度 2 写作 0x02 0x00。
	if got := buf.Bytes()[:2]; got[0] != 0x02 || got[1] != 0x00 {
		t.Errorf("little-endian header = % x, want 02 00", got)
	}
}

func TestConcurrentWriter(t *testing.T) {
	c := New(WithHeaderSize(4))
	var buf bytes.Buffer
	w := c.NewWriter(&buf)

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if err := w.WriteFrame([]byte("payload")); err != nil {
				t.Errorf("WriteFrame: %v", err)
			}
		}()
	}
	wg.Wait()

	// 应能干净读出 n 帧,证明写入没有交错错帧。
	r := c.NewReader(&buf)
	for i := 0; i < n; i++ {
		got, err := r.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if !bytes.Equal(got, []byte("payload")) {
			t.Fatalf("frame #%d = %q, want payload", i, got)
		}
	}
}
