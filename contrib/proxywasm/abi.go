package proxywasm

// Proxy-Wasm ABI v0.2.1 常量定义。

// WasmResult 是 host function 的返回状态码。
type WasmResult uint32

const (
	WasmResultOk                 WasmResult = 0
	WasmResultNotFound           WasmResult = 1
	WasmResultBadArgument        WasmResult = 2
	WasmResultSerializationError WasmResult = 3
	WasmResultParseFailure       WasmResult = 4
	WasmResultBadExpression      WasmResult = 5
	WasmResultInvalidMemAccess   WasmResult = 6
	WasmResultEmpty              WasmResult = 7
	WasmResultCASMismatch        WasmResult = 8
	WasmResultResultNotFound     WasmResult = 9
	WasmResultInternalFailure    WasmResult = 10
	WasmResultBrokenConnection   WasmResult = 11
	WasmResultUnimplemented      WasmResult = 12
)

// Action 是 guest 的 proxy_on_*_headers/body 回调返回值。
type Action uint32

const (
	ActionContinue Action = 0
	ActionPause    Action = 1
)

// LogLevel 是日志级别。
type LogLevel uint32

const (
	LogTrace    LogLevel = 0
	LogDebug    LogLevel = 1
	LogInfo     LogLevel = 2
	LogWarn     LogLevel = 3
	LogError    LogLevel = 4
	LogCritical LogLevel = 5
)

// MapType 标识 header/trailer map 类型。
type MapType uint32

const (
	MapTypeHTTPRequestHeaders       MapType = 0
	MapTypeHTTPRequestTrailers      MapType = 1
	MapTypeHTTPResponseHeaders      MapType = 2
	MapTypeHTTPResponseTrailers     MapType = 3
	MapTypeGRPCReceiveInitialMeta   MapType = 4
	MapTypeGRPCReceiveTrailingMeta  MapType = 5
	MapTypeHTTPCallResponseHeaders  MapType = 6
	MapTypeHTTPCallResponseTrailers MapType = 7
)

// BufferType 标识 buffer 类型。
type BufferType uint32

const (
	BufferTypeHTTPRequestBody      BufferType = 0
	BufferTypeHTTPResponseBody     BufferType = 1
	BufferTypeDownstreamData       BufferType = 2
	BufferTypeUpstreamData         BufferType = 3
	BufferTypeHTTPCallResponseBody BufferType = 4
	BufferTypeGRPCReceiveBuffer    BufferType = 5
	BufferTypeVMConfiguration      BufferType = 6
	BufferTypePluginConfiguration  BufferType = 7
	BufferTypeForeignFuncArgs      BufferType = 8
)

// StreamType 用于 proxy_continue_stream / proxy_close_stream。
type StreamType uint32

const (
	StreamTypeRequest  StreamType = 0
	StreamTypeResponse StreamType = 1
)

// MetricType 用于 proxy_define_metric（stub）。
type MetricType uint32

const (
	MetricTypeCounter   MetricType = 0
	MetricTypeGauge     MetricType = 1
	MetricTypeHistogram MetricType = 2
)
