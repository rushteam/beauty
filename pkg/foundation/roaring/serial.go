package roaring

import (
	"encoding/binary"
	"fmt"
	"io"
)

// 本文件给 Bitmap 加二进制序列化,便于持久化(落盘/入库)与网络传输(多节点交换位图)。
//
// 采用本仓自定义的紧凑格式(非官方 RoaringFormatSpec,保持纯标准库、实现简单),小端字节序:
//
//	magic  "RBM1"            (4 字节)
//	count  uint32            桶数
//	repeat count 次:
//	  key    uint16          高 16 位桶键
//	  typ    uint8           0=array 1=bitmap 2=run
//	  card   uint32          桶内基数
//	  payload:
//	    array : card × uint16
//	    bitmap: 1024 × uint64
//	    run   : uint16 游程数,然后每游程 (uint16 start, uint16 length)
//
// 序列化前可先 RunOptimize() 压缩连续区间。反序列化得到的位图与原位图等价。

var magic = [4]byte{'R', 'B', 'M', '1'}

// MarshalBinary 实现 encoding.BinaryMarshaler。
func (b *Bitmap) MarshalBinary() ([]byte, error) {
	var buf []byte
	buf = append(buf, magic[:]...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(b.keys)))
	for i, key := range b.keys {
		c := b.containers[i]
		buf = binary.LittleEndian.AppendUint16(buf, key)
		buf = append(buf, byte(c.typ))
		buf = binary.LittleEndian.AppendUint32(buf, uint32(c.cardinality()))
		switch c.typ {
		case typeArray:
			for _, v := range c.arr {
				buf = binary.LittleEndian.AppendUint16(buf, v)
			}
		case typeBitmap:
			for _, w := range c.bitmap {
				buf = binary.LittleEndian.AppendUint64(buf, w)
			}
		case typeRun:
			buf = binary.LittleEndian.AppendUint16(buf, uint16(len(c.runs)))
			for _, r := range c.runs {
				buf = binary.LittleEndian.AppendUint16(buf, r.start)
				buf = binary.LittleEndian.AppendUint16(buf, r.length)
			}
		}
	}
	return buf, nil
}

// UnmarshalBinary 实现 encoding.BinaryUnmarshaler,用序列化数据重建位图(覆盖原有内容)。
func (b *Bitmap) UnmarshalBinary(data []byte) error {
	r := &reader{data: data}
	var m [4]byte
	if !r.bytes(m[:]) || m != magic {
		return fmt.Errorf("roaring: bad magic")
	}
	count, ok := r.u32()
	if !ok {
		return io.ErrUnexpectedEOF
	}
	keys := make([]uint16, 0, count)
	containers := make([]*container, 0, count)
	for i := uint32(0); i < count; i++ {
		key, ok1 := r.u16()
		typ, ok2 := r.u8()
		card, ok3 := r.u32()
		if !ok1 || !ok2 || !ok3 {
			return io.ErrUnexpectedEOF
		}
		c := &container{typ: containerType(typ), card: int(card)}
		switch c.typ {
		case typeArray:
			c.arr = make([]uint16, card)
			for j := range c.arr {
				if c.arr[j], ok = r.u16(); !ok {
					return io.ErrUnexpectedEOF
				}
			}
		case typeBitmap:
			c.bitmap = make([]uint64, 1024)
			for j := range c.bitmap {
				if c.bitmap[j], ok = r.u64(); !ok {
					return io.ErrUnexpectedEOF
				}
			}
		case typeRun:
			nruns, ok4 := r.u16()
			if !ok4 {
				return io.ErrUnexpectedEOF
			}
			c.runs = make([]rle, nruns)
			for j := range c.runs {
				s, ok5 := r.u16()
				l, ok6 := r.u16()
				if !ok5 || !ok6 {
					return io.ErrUnexpectedEOF
				}
				c.runs[j] = rle{start: s, length: l}
			}
		default:
			return fmt.Errorf("roaring: unknown container type %d", typ)
		}
		keys = append(keys, key)
		containers = append(containers, c)
	}
	b.keys, b.containers = keys, containers
	return nil
}

// reader 是一个简单的小端二进制游标。
type reader struct {
	data []byte
	pos  int
}

func (r *reader) bytes(dst []byte) bool {
	if r.pos+len(dst) > len(r.data) {
		return false
	}
	copy(dst, r.data[r.pos:])
	r.pos += len(dst)
	return true
}

func (r *reader) u8() (uint8, bool) {
	if r.pos+1 > len(r.data) {
		return 0, false
	}
	v := r.data[r.pos]
	r.pos++
	return v, true
}

func (r *reader) u16() (uint16, bool) {
	if r.pos+2 > len(r.data) {
		return 0, false
	}
	v := binary.LittleEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v, true
}

func (r *reader) u32() (uint32, bool) {
	if r.pos+4 > len(r.data) {
		return 0, false
	}
	v := binary.LittleEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return v, true
}

func (r *reader) u64() (uint64, bool) {
	if r.pos+8 > len(r.data) {
		return 0, false
	}
	v := binary.LittleEndian.Uint64(r.data[r.pos:])
	r.pos += 8
	return v, true
}
