package proxywasm

import "net/http"

// pluginState 管理单个 wasm 实例的上下文层级和当前活跃状态。
// Proxy-Wasm 上下文模型: Root(VM) → Plugin → Stream(per-request)。
type pluginState struct {
	nextContextID   uint32
	rootContextID   uint32
	pluginContextID uint32
	activeContextID uint32

	contexts map[uint32]*contextState

	vmConfig     []byte
	pluginConfig []byte
	logLevel     LogLevel

	// 自定义 properties（全局，可按需覆盖）
	properties map[string][]byte

	// Runtime 级别共享资源(由 Runtime 持有,所有实例共享)
	sharedData   *sharedDataStore
	metrics      *metricStore
	sharedQueue  *SharedQueue
	foreignFuncs *ForeignFuncRegistry
	dispatcher   Dispatcher

	// Tick 定时器
	tickPeriodMs uint32
	lastTickTime int64 // UnixMilli

	// Pending callouts: 当前回调期间 guest 发起的异步操作,
	// 待当前回调返回后依次分发 proxy_on_http_call_response / proxy_on_grpc_receive 等。
	pendingCallouts []*pendingCallout
	nextCalloutID   uint32

	// gRPC streams (unary 简化实现)
	grpcStreams  map[uint32]*grpcStreamState
	nextStreamID uint32
}

// contextState 表示一个 stream/plugin/root context 的运行时状态。
type contextState struct {
	parentID uint32

	// HTTP 请求阶段
	requestHeaders  http.Header
	requestBody     []byte
	requestTrailers http.Header
	requestPath     string
	requestMethod   string

	// HTTP 响应阶段
	responseHeaders  http.Header
	responseBody     []byte
	responseTrailers http.Header
	responseStatus   int

	// send_local_response 结果
	localResponse *localResp

	// 每上下文 properties
	properties map[string][]byte

	// 流控状态
	requestDone  bool
	responseDone bool
}

// localResp 是 proxy_send_local_response 的结果。
type localResp struct {
	status  int
	headers http.Header
	body    []byte
}

func newPluginState(vmConfig, pluginConfig []byte, logLevel LogLevel, props map[string][]byte, sd *sharedDataStore, ms *metricStore, sq *SharedQueue, ff *ForeignFuncRegistry, disp Dispatcher) *pluginState {
	ps := &pluginState{
		nextContextID: 1,
		contexts:      make(map[uint32]*contextState),
		vmConfig:      vmConfig,
		pluginConfig:  pluginConfig,
		logLevel:      logLevel,
		properties:    props,
		sharedData:    sd,
		metrics:       ms,
		sharedQueue:   sq,
		foreignFuncs:  ff,
		dispatcher:    disp,
		nextCalloutID: 1,
		nextStreamID:  1,
		grpcStreams:   make(map[uint32]*grpcStreamState),
	}
	return ps
}

func (ps *pluginState) allocContextID() uint32 {
	id := ps.nextContextID
	ps.nextContextID++
	return id
}

func (ps *pluginState) activeContext() *contextState {
	if cs, ok := ps.contexts[ps.activeContextID]; ok {
		return cs
	}
	return nil
}

func (ps *pluginState) getContext(id uint32) *contextState {
	return ps.contexts[id]
}
