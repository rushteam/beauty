package bedrock

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"testing"
)

// encodeFrame 构造一个 event-stream 帧(CRC 置零,解码器不校验)。
func encodeFrame(headers map[string]string, payload []byte) []byte {
	var hb bytes.Buffer
	for name, val := range headers {
		hb.WriteByte(byte(len(name)))
		hb.WriteString(name)
		hb.WriteByte(hdrTypeBytes) // string
		var vl [2]byte
		binary.BigEndian.PutUint16(vl[:], uint16(len(val)))
		hb.Write(vl[:])
		hb.WriteString(val)
	}
	headerBytes := hb.Bytes()
	total := overhead + len(headerBytes) + len(payload)

	var out bytes.Buffer
	var u32 [4]byte
	binary.BigEndian.PutUint32(u32[:], uint32(total))
	out.Write(u32[:])
	binary.BigEndian.PutUint32(u32[:], uint32(len(headerBytes)))
	out.Write(u32[:])
	out.Write([]byte{0, 0, 0, 0}) // prelude CRC (ignored)
	out.Write(headerBytes)
	out.Write(payload)
	out.Write([]byte{0, 0, 0, 0}) // message CRC (ignored)
	return out.Bytes()
}

func TestDecoder_RoundTrip(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(encodeFrame(map[string]string{":event-type": "chunk", ":message-type": "event"}, []byte(`{"a":1}`)))
	stream.Write(encodeFrame(map[string]string{":event-type": "chunk"}, []byte(`{"b":2}`)))

	d := NewDecoder(&stream)

	f1, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	if f1.EventType() != "chunk" || f1.MessageType() != "event" || string(f1.Payload) != `{"a":1}` {
		t.Fatalf("frame1 wrong: %#v payload=%s", f1.Headers, f1.Payload)
	}

	f2, err := d.Next()
	if err != nil {
		t.Fatal(err)
	}
	if string(f2.Payload) != `{"b":2}` {
		t.Fatalf("frame2 payload wrong: %s", f2.Payload)
	}

	if _, err := d.Next(); err != io.EOF {
		t.Fatalf("expected io.EOF at end, got %v", err)
	}
}

func TestDecoder_ExceptionFrame(t *testing.T) {
	f := encodeFrame(map[string]string{
		":message-type":   "exception",
		":exception-type": "throttlingException",
	}, []byte(`{"message":"slow down"}`))
	frame, err := NewDecoder(bytes.NewReader(f)).Next()
	if err != nil {
		t.Fatal(err)
	}
	if frame.MessageType() != "exception" || frame.ExceptionType() != "throttlingException" {
		t.Fatalf("exception headers wrong: %#v", frame.Headers)
	}
}

func TestDecoder_TruncatedFrame(t *testing.T) {
	full := encodeFrame(map[string]string{":event-type": "chunk"}, []byte(`{"a":1}`))
	// 砍掉尾部,模拟半个帧
	d := NewDecoder(bytes.NewReader(full[:len(full)-3]))
	if _, err := d.Next(); err != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}
}

func TestDecodeChunkBytes(t *testing.T) {
	inner := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`
	payload := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(inner)) + `"}`)
	got, err := DecodeChunkBytes(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != inner {
		t.Fatalf("decoded chunk bytes wrong: %s", got)
	}
}

func TestParseHeaders_SkipsNonStringTypes(t *testing.T) {
	// 手工拼:一个 int32 header(type=4)后跟一个 string header,确认能跳过 int32 正确读到 string。
	var b bytes.Buffer
	b.WriteByte(byte(len("n")))
	b.WriteString("n")
	b.WriteByte(4) // int32
	b.Write([]byte{0, 0, 0, 7})
	b.WriteByte(byte(len(":event-type")))
	b.WriteString(":event-type")
	b.WriteByte(hdrTypeBytes)
	b.Write([]byte{0, byte(len("chunk"))})
	b.WriteString("chunk")

	h := parseHeaders(b.Bytes())
	if h[":event-type"] != "chunk" {
		t.Fatalf("failed to skip int32 header and read string: %#v", h)
	}
}
