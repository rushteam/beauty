package proxywasm

import (
	"encoding/binary"
	"net/http"
)

// encodeHeaderMap 将 header 编码为 Proxy-Wasm 二进制格式。
// 格式(小端): [count:u32] [key1_len:u32 val1_len:u32 ...] [key1\0 val1\0 ...]
func encodeHeaderMap(h http.Header) []byte {
	pairs := headerToPairs(h)
	return encodePairs(pairs)
}

// decodeHeaderMap 从 Proxy-Wasm 二进制格式解码为 kv pairs。
func decodeHeaderMap(data []byte) [][2]string {
	if len(data) < 4 {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(data[:4]))
	if count == 0 {
		return nil
	}
	headerSize := 4 + count*8
	if len(data) < headerSize {
		return nil
	}
	pairs := make([][2]string, count)
	offset := 4
	var dataOffset int = headerSize
	for i := 0; i < count; i++ {
		keyLen := int(binary.LittleEndian.Uint32(data[offset:]))
		valLen := int(binary.LittleEndian.Uint32(data[offset+4:]))
		offset += 8

		if dataOffset+keyLen+1+valLen+1 > len(data) {
			return pairs[:i]
		}
		key := string(data[dataOffset : dataOffset+keyLen])
		dataOffset += keyLen + 1 // skip NUL
		val := string(data[dataOffset : dataOffset+valLen])
		dataOffset += valLen + 1 // skip NUL
		pairs[i] = [2]string{key, val}
	}
	return pairs
}

// encodePairs 编码 key-value pairs 为 Proxy-Wasm 二进制格式。
func encodePairs(pairs [][2]string) []byte {
	count := len(pairs)
	headerSize := 4 + count*8
	dataSize := 0
	for _, p := range pairs {
		dataSize += len(p[0]) + 1 + len(p[1]) + 1
	}
	buf := make([]byte, headerSize+dataSize)
	binary.LittleEndian.PutUint32(buf[:4], uint32(count))
	offset := 4
	for _, p := range pairs {
		binary.LittleEndian.PutUint32(buf[offset:], uint32(len(p[0])))
		binary.LittleEndian.PutUint32(buf[offset+4:], uint32(len(p[1])))
		offset += 8
	}
	for _, p := range pairs {
		copy(buf[offset:], p[0])
		offset += len(p[0])
		buf[offset] = 0
		offset++
		copy(buf[offset:], p[1])
		offset += len(p[1])
		buf[offset] = 0
		offset++
	}
	return buf
}

// headerToPairs 将 http.Header 转为有序 key-value pairs（多值展开为多条）。
func headerToPairs(h http.Header) [][2]string {
	var pairs [][2]string
	for k, vs := range h {
		for _, v := range vs {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	return pairs
}

// pairsToHeader 将 pairs 转为 http.Header。
func pairsToHeader(pairs [][2]string) http.Header {
	h := make(http.Header, len(pairs))
	for _, p := range pairs {
		h.Add(p[0], p[1])
	}
	return h
}

// headerGet 从 http.Header 中获取值，兼容 Proxy-Wasm 的小写 key 和伪头（如 :method）。
// 先尝试原始 key，再尝试 CanonicalHeaderKey，最后做大小写无关的线性搜索。
func headerGet(h http.Header, key string) (string, bool) {
	if vs, ok := h[key]; ok && len(vs) > 0 {
		return vs[0], true
	}
	canonical := http.CanonicalHeaderKey(key)
	if vs, ok := h[canonical]; ok && len(vs) > 0 {
		return vs[0], true
	}
	for k, vs := range h {
		if http.CanonicalHeaderKey(k) == canonical && len(vs) > 0 {
			return vs[0], true
		}
	}
	return "", false
}
