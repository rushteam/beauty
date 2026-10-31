package redis

import (
	"encoding/binary"
	"strings"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// envelopeMagic 标记带 Key / Headers 的信封。以 0x00 开头，正常文本 / JSON 负载不会与之冲突。
const envelopeMagic = "\x00bmq\x01"

// encodeMessage 把 mq.Message 编为 Pub/Sub 负载：
// Key 与 Headers 都为空时即 Body 原文；否则为
// magic | uvarint(len key) key | uvarint(n) { uvarint(len k) k uvarint(len v) v }*n | body。
func encodeMessage(msg mq.Message) []byte {
	if msg.Key == "" && len(msg.Headers) == 0 {
		return msg.Body
	}
	size := len(envelopeMagic) + binary.MaxVarintLen64*(2+2*len(msg.Headers)) + len(msg.Key) + len(msg.Body)
	for k, v := range msg.Headers {
		size += len(k) + len(v)
	}
	buf := make([]byte, 0, size)
	buf = append(buf, envelopeMagic...)
	buf = appendString(buf, msg.Key)
	buf = binary.AppendUvarint(buf, uint64(len(msg.Headers)))
	for k, v := range msg.Headers {
		buf = appendString(buf, k)
		buf = appendString(buf, v)
	}
	return append(buf, msg.Body...)
}

// decodeMessage 解析负载；不带 magic 或格式损坏时整体视为 Body，与非 beauty 发布方互通。
func decodeMessage(topic, payload string) mq.Message {
	raw := mq.Message{Topic: topic, Body: []byte(payload)}
	if !strings.HasPrefix(payload, envelopeMagic) {
		return raw
	}
	data := []byte(payload[len(envelopeMagic):])
	key, data, ok := readString(data)
	if !ok {
		return raw
	}
	n, w := binary.Uvarint(data)
	if w <= 0 || n > uint64(len(data)) {
		return raw
	}
	data = data[w:]
	var headers map[string]string
	if n > 0 {
		headers = make(map[string]string, n)
	}
	for range n {
		var k, v string
		if k, data, ok = readString(data); !ok {
			return raw
		}
		if v, data, ok = readString(data); !ok {
			return raw
		}
		headers[k] = v
	}
	return mq.Message{Topic: topic, Key: key, Body: data, Headers: headers}
}

func appendString(buf []byte, s string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func readString(data []byte) (string, []byte, bool) {
	n, w := binary.Uvarint(data)
	if w <= 0 || n > uint64(len(data)-w) {
		return "", nil, false
	}
	end := w + int(n)
	return string(data[w:end]), data[end:], true
}
