package proxywasm

// buffers.go 实现 BufferType → 实际数据的映射读写。

// getBuffer 根据 BufferType 和当前活跃 context 返回对应 buffer 的引用。
func (ps *pluginState) getBuffer(bt BufferType) []byte {
	switch bt {
	case BufferTypeVMConfiguration:
		return ps.vmConfig
	case BufferTypePluginConfiguration:
		return ps.pluginConfig
	case BufferTypeHTTPRequestBody:
		if cs := ps.activeContext(); cs != nil {
			return cs.requestBody
		}
	case BufferTypeHTTPResponseBody:
		if cs := ps.activeContext(); cs != nil {
			return cs.responseBody
		}
	}
	return nil
}

// setBuffer 根据 BufferType 设置对应 buffer。
func (ps *pluginState) setBuffer(bt BufferType, data []byte) bool {
	switch bt {
	case BufferTypeHTTPRequestBody:
		if cs := ps.activeContext(); cs != nil {
			cs.requestBody = data
			return true
		}
	case BufferTypeHTTPResponseBody:
		if cs := ps.activeContext(); cs != nil {
			cs.responseBody = data
			return true
		}
	}
	return false
}

// getBufferSize 返回 buffer 的当前大小。
func (ps *pluginState) getBufferSize(bt BufferType) int {
	buf := ps.getBuffer(bt)
	return len(buf)
}
