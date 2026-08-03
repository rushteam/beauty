package bedrock

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// AWS event-stream(vnd.amazon.eventstream)帧解码,纯标准库。
// Bedrock 的 invoke-with-response-stream 用该二进制协议承载流式响应,与 model 家族无关。
//
// 帧布局(大端):
//
//	total_length  : uint32   整帧字节数
//	headers_length: uint32   headers 区字节数
//	prelude_crc   : uint32   前 8 字节的 CRC32(本实现不校验)
//	headers       : headers_length 字节
//	payload       : total_length-headers_length-16 字节
//	message_crc   : uint32   整帧 CRC32(本实现不校验)
//
// header 项布局:name_len(1) | name | value_type(1) | value。
// 只解析 string 型(value_type=7:len(2)+bytes)——Bedrock 的 :event-type / :content-type /
// :message-type / :exception-type 都是 string,足够区分 chunk 与异常帧。其余类型跳过其值。
const (
	preludeLen   = 8 // total_length + headers_length
	preludeCRC   = 4 // 跳过的 prelude CRC
	messageCRC   = 4 // 帧尾 message CRC
	hdrTypeBytes = 7 // string header value type
	maxFrameSize = 24 * 1024 * 1024

	overhead = preludeLen + preludeCRC + messageCRC // 帧固定开销(不含 headers/payload)
)

// Frame 是解出的一个 event-stream 帧。
type Frame struct {
	Headers map[string]string // 仅含 string 型 header(如 :event-type)
	Payload []byte
}

// EventType 返回 :event-type 头(如 "chunk"),无则空串。
func (f Frame) EventType() string { return f.Headers[":event-type"] }

// MessageType 返回 :message-type 头(如 "event" / "exception"),无则空串。
func (f Frame) MessageType() string { return f.Headers[":message-type"] }

// ExceptionType 返回 :exception-type 头(异常帧才有)。
func (f Frame) ExceptionType() string { return f.Headers[":exception-type"] }

// Decoder 从 io.Reader 逐帧解出 event-stream 消息。
type Decoder struct {
	r io.Reader
}

// NewDecoder 构造解码器。
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: r} }

// Next 读取下一帧。流结束返回 io.EOF。
func (d *Decoder) Next() (Frame, error) {
	var prelude [preludeLen]byte
	if _, err := io.ReadFull(d.r, prelude[:]); err != nil {
		return Frame{}, err // 干净结束时这里是 io.EOF
	}
	total := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])

	if total < uint32(overhead)+headersLen || total > maxFrameSize {
		return Frame{}, fmt.Errorf("bedrock: eventstream: bad frame length total=%d headers=%d", total, headersLen)
	}

	// 读掉 prelude CRC + headers + payload + message CRC(prelude 已读 8 字节)
	rest := make([]byte, total-preludeLen)
	if _, err := io.ReadFull(d.r, rest); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}

	headersStart := preludeCRC
	headersEnd := headersStart + int(headersLen)
	payloadEnd := len(rest) - messageCRC
	if headersEnd > payloadEnd {
		return Frame{}, fmt.Errorf("bedrock: eventstream: headers overflow frame")
	}

	headers := parseHeaders(rest[headersStart:headersEnd])
	payload := rest[headersEnd:payloadEnd]
	return Frame{Headers: headers, Payload: payload}, nil
}

// parseHeaders 解析 headers 区,只收集 string 型(value_type=7),其余类型按其固定/变长跳过。
func parseHeaders(b []byte) map[string]string {
	headers := map[string]string{}
	i := 0
	for i < len(b) {
		nameLen := int(b[i])
		i++
		if i+nameLen > len(b) {
			break
		}
		name := string(b[i : i+nameLen])
		i += nameLen
		if i >= len(b) {
			break
		}
		vtype := b[i]
		i++
		switch vtype {
		case hdrTypeBytes: // string:2 字节长度 + 值
			if i+2 > len(b) {
				return headers
			}
			vlen := int(binary.BigEndian.Uint16(b[i : i+2]))
			i += 2
			if i+vlen > len(b) {
				return headers
			}
			headers[name] = string(b[i : i+vlen])
			i += vlen
		case 6: // byte array:2 字节长度 + 值(跳过)
			if i+2 > len(b) {
				return headers
			}
			vlen := int(binary.BigEndian.Uint16(b[i : i+2]))
			i += 2 + vlen
		case 0, 1: // bool true/false:无值
		case 2: // byte
			i++
		case 3: // int16
			i += 2
		case 4: // int32
			i += 4
		case 5, 8: // int64 / timestamp
			i += 8
		case 9: // uuid
			i += 16
		default:
			return headers // 未知类型,无法安全跳过,停止解析
		}
	}
	return headers
}

// chunkPayload 是 Bedrock chunk 帧 payload 的外层:{"bytes":"<base64>"},
// base64 解码后是 model 家族的事件 JSON。
type chunkPayload struct {
	Bytes []byte `json:"bytes"` // encoding/json 自动做 base64 解码(标准编码)
}

// DecodeChunkBytes 从 chunk 帧 payload 取出内层家族事件 JSON。
func DecodeChunkBytes(payload []byte) ([]byte, error) {
	var cp chunkPayload
	if err := json.Unmarshal(payload, &cp); err != nil {
		return nil, err
	}
	return cp.Bytes, nil
}
