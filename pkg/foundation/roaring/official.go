package roaring

import (
	"encoding/binary"
	"fmt"
	"io"
)

// 本文件实现**官方 RoaringFormatSpec**(github.com/RoaringBitmap/RoaringFormatSpec)的序列化,
// 使本包的位图能与 CRoaring(C/C++)、RoaringBitmap(Java)、pyroaring(Python)、roaring(Go,
// RoaringBitmap/roaring)等生态**跨语言互读互写**(Kafka/Parquet/落盘共享)。
//
// 与本包私有格式(serial.go 的 MarshalBinary)并存:私有格式更简单、仅自用;官方格式用于
// 与外部系统交换。两者可按场景各取所需。
//
// 布局(小端):
//
//	cookie:
//	  无 run 容器 → uint32 SERIAL_COOKIE_NO_RUNCONTAINER, uint32 容器数
//	  有 run 容器 → uint32 (SERIAL_COOKIE | ((容器数-1)<<16)), 然后 ceil(容器数/8) 字节的 run 标志位图
//	descriptive header: 每容器 (uint16 key, uint16 基数-1)
//	offset header(当「无 run」或「容器数>=4」时存在): 每容器 uint32 数据偏移
//	container data:
//	  run   : uint16 游程数, 每游程 (uint16 start, uint16 游程长度-1)
//	  array : 基数 × uint16
//	  bitmap: 1024 × uint64

const (
	serialCookieNoRun = 12346
	serialCookie      = 12347
	noOffsetThreshold = 4
	// 官方 DEFAULT_MAX_SIZE:基数 <= 此值用 array,否则 bitmap。与 arrayThreshold 一致。
)

// MarshalRoaring 按官方 RoaringFormatSpec 序列化(跨语言互操作)。序列化前可先 RunOptimize()。
func (b *Bitmap) MarshalRoaring() ([]byte, error) {
	size := len(b.keys)
	hasRun := false
	for _, c := range b.containers {
		if c.isRun() {
			hasRun = true
			break
		}
	}

	// 先生成每个容器的数据负载,便于计算偏移。
	payloads := make([][]byte, size)
	for i, c := range b.containers {
		payloads[i] = marshalContainer(c)
	}

	// 头部大小。
	headerLen := 0
	if hasRun {
		headerLen += 4 + (size+7)/8 // cookie + run 标志位图
	} else {
		headerLen += 4 + 4 // cookie + size
	}
	headerLen += size * 4 // descriptive header
	withOffsets := !hasRun || size >= noOffsetThreshold
	if withOffsets {
		headerLen += size * 4 // offset header
	}

	var buf []byte
	if hasRun {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(serialCookie|((size-1)<<16)))
		runFlags := make([]byte, (size+7)/8)
		for i, c := range b.containers {
			if c.isRun() {
				runFlags[i/8] |= 1 << uint(i%8)
			}
		}
		buf = append(buf, runFlags...)
	} else {
		buf = binary.LittleEndian.AppendUint32(buf, serialCookieNoRun)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(size))
	}
	// descriptive header
	for i, key := range b.keys {
		buf = binary.LittleEndian.AppendUint16(buf, key)
		buf = binary.LittleEndian.AppendUint16(buf, uint16(b.containers[i].cardinality()-1))
	}
	// offset header
	if withOffsets {
		off := headerLen
		for i := 0; i < size; i++ {
			buf = binary.LittleEndian.AppendUint32(buf, uint32(off))
			off += len(payloads[i])
		}
	}
	// container data
	for _, p := range payloads {
		buf = append(buf, p...)
	}
	return buf, nil
}

// marshalContainer 把单个容器序列化为官方容器负载。
func marshalContainer(c *container) []byte {
	var p []byte
	switch c.typ {
	case typeRun:
		p = binary.LittleEndian.AppendUint16(p, uint16(len(c.runs)))
		for _, r := range c.runs {
			p = binary.LittleEndian.AppendUint16(p, r.start)
			p = binary.LittleEndian.AppendUint16(p, r.length-1) // 官方存游程长度-1
		}
	case typeArray:
		for _, v := range c.arr {
			p = binary.LittleEndian.AppendUint16(p, v)
		}
	case typeBitmap:
		for _, w := range c.bitmap {
			p = binary.LittleEndian.AppendUint64(p, w)
		}
	}
	return p
}

// UnmarshalRoaring 解析官方 RoaringFormatSpec 数据(覆盖原有内容),可读 CRoaring/Java 等写出的位图。
func (b *Bitmap) UnmarshalRoaring(data []byte) error {
	r := &reader{data: data}
	cookie, ok := r.u32()
	if !ok {
		return io.ErrUnexpectedEOF
	}
	var size int
	hasRun := false
	var runFlags []byte
	if cookie&0xFFFF == serialCookie {
		hasRun = true
		size = int((cookie>>16)&0xFFFF) + 1
		runFlags = make([]byte, (size+7)/8)
		if !r.bytes(runFlags) {
			return io.ErrUnexpectedEOF
		}
	} else if cookie == serialCookieNoRun {
		sz, ok := r.u32()
		if !ok {
			return io.ErrUnexpectedEOF
		}
		size = int(sz)
	} else {
		return fmt.Errorf("roaring: unknown cookie %d", cookie)
	}

	keys := make([]uint16, size)
	cards := make([]int, size)
	for i := 0; i < size; i++ {
		k, ok1 := r.u16()
		cm1, ok2 := r.u16()
		if !ok1 || !ok2 {
			return io.ErrUnexpectedEOF
		}
		keys[i] = k
		cards[i] = int(cm1) + 1
	}
	// offset header:存在时跳过(本实现顺序读取,偏移仅作交叉校验用,这里忽略)。
	if !hasRun || size >= noOffsetThreshold {
		for i := 0; i < size; i++ {
			if _, ok := r.u32(); !ok {
				return io.ErrUnexpectedEOF
			}
		}
	}

	containers := make([]*container, size)
	for i := 0; i < size; i++ {
		isRun := hasRun && runFlags[i/8]&(1<<uint(i%8)) != 0
		c, err := unmarshalContainer(r, cards[i], isRun)
		if err != nil {
			return err
		}
		containers[i] = c
	}
	b.keys, b.containers = keys, containers
	return nil
}

// unmarshalContainer 读单个官方容器负载。
func unmarshalContainer(r *reader, card int, isRun bool) (*container, error) {
	c := &container{card: card}
	switch {
	case isRun:
		nruns, ok := r.u16()
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		c.typ = typeRun
		c.runs = make([]rle, nruns)
		total := 0
		for j := range c.runs {
			s, ok1 := r.u16()
			lm1, ok2 := r.u16()
			if !ok1 || !ok2 {
				return nil, io.ErrUnexpectedEOF
			}
			c.runs[j] = rle{start: s, length: lm1 + 1}
			total += int(lm1) + 1
		}
		c.card = total
	case card <= arrayThreshold:
		c.typ = typeArray
		c.arr = make([]uint16, card)
		for j := range c.arr {
			v, ok := r.u16()
			if !ok {
				return nil, io.ErrUnexpectedEOF
			}
			c.arr[j] = v
		}
	default:
		c.typ = typeBitmap
		c.bitmap = make([]uint64, 1024)
		for j := range c.bitmap {
			w, ok := r.u64()
			if !ok {
				return nil, io.ErrUnexpectedEOF
			}
			c.bitmap[j] = w
		}
	}
	return c, nil
}
