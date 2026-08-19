package proxywasm

import "strings"

// properties.go 实现基础 property path 解析。
// Proxy-Wasm property path 用 NUL 分隔段: "request\x00path" → ["request", "path"]。

// getProperty 解析 property path 并返回值。
func (ps *pluginState) getProperty(path []byte) ([]byte, WasmResult) {
	segments := splitPropertyPath(path)
	if len(segments) == 0 {
		return nil, WasmResultBadArgument
	}

	cs := ps.activeContext()

	// 先查 context 级别 properties
	if cs != nil && cs.properties != nil {
		key := string(path)
		if v, ok := cs.properties[key]; ok {
			return v, WasmResultOk
		}
	}

	// 全局 properties
	if ps.properties != nil {
		key := string(path)
		if v, ok := ps.properties[key]; ok {
			return v, WasmResultOk
		}
	}

	// 内置 properties
	switch segments[0] {
	case "request":
		return ps.getRequestProperty(segments[1:], cs)
	case "response":
		return ps.getResponseProperty(segments[1:], cs)
	case "plugin_name":
		return []byte("proxywasm"), WasmResultOk
	case "plugin_vm_id":
		return []byte(""), WasmResultOk
	case "node":
		return ps.getNodeProperty(segments[1:])
	}

	return nil, WasmResultNotFound
}

// setProperty 设置 property。
func (ps *pluginState) setProperty(path []byte, value []byte) WasmResult {
	cs := ps.activeContext()
	if cs == nil {
		return WasmResultBadArgument
	}
	if cs.properties == nil {
		cs.properties = make(map[string][]byte)
	}
	cs.properties[string(path)] = value
	return WasmResultOk
}

func (ps *pluginState) getRequestProperty(segments []string, cs *contextState) ([]byte, WasmResult) {
	if cs == nil || len(segments) == 0 {
		return nil, WasmResultNotFound
	}
	switch segments[0] {
	case "path":
		return []byte(cs.requestPath), WasmResultOk
	case "method":
		return []byte(cs.requestMethod), WasmResultOk
	case "host":
		if cs.requestHeaders != nil {
			return []byte(cs.requestHeaders.Get("Host")), WasmResultOk
		}
	case "scheme":
		return []byte("http"), WasmResultOk
	case "protocol":
		return []byte("HTTP/1.1"), WasmResultOk
	case "total_size":
		size := len(cs.requestBody)
		return encodeUint64LE(uint64(size)), WasmResultOk
	}
	return nil, WasmResultNotFound
}

func (ps *pluginState) getResponseProperty(segments []string, cs *contextState) ([]byte, WasmResult) {
	if cs == nil || len(segments) == 0 {
		return nil, WasmResultNotFound
	}
	switch segments[0] {
	case "code":
		return encodeUint64LE(uint64(cs.responseStatus)), WasmResultOk
	}
	return nil, WasmResultNotFound
}

func (ps *pluginState) getNodeProperty(segments []string) ([]byte, WasmResult) {
	if len(segments) == 0 {
		return nil, WasmResultNotFound
	}
	switch segments[0] {
	case "id":
		return []byte("beauty-node"), WasmResultOk
	case "cluster":
		return []byte("beauty-cluster"), WasmResultOk
	}
	return nil, WasmResultNotFound
}

func splitPropertyPath(path []byte) []string {
	s := string(path)
	return strings.Split(s, "\x00")
}

func encodeUint64LE(v uint64) []byte {
	buf := make([]byte, 8)
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
	buf[4] = byte(v >> 32)
	buf[5] = byte(v >> 40)
	buf[6] = byte(v >> 48)
	buf[7] = byte(v >> 56)
	return buf
}
