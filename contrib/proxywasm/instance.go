package proxywasm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tetratelabs/wazero/api"
)

type ctxKeyType struct{}

var ctxKey = ctxKeyType{}

func withState(ctx context.Context, s *pluginState) context.Context {
	return context.WithValue(ctx, ctxKey, s)
}

func stateFromCtx(ctx context.Context) *pluginState {
	if s, ok := ctx.Value(ctxKey).(*pluginState); ok {
		return s
	}
	return nil
}

// instance 封装一个已初始化完毕(VM start + Plugin configure)的 wasm 实例,
// 可反复处理 HTTP stream context。非并发安全,由 pool 保证每次只有一个 goroutine 使用。
type instance struct {
	mod   api.Module
	state *pluginState
}

// callCtx 构造带 state 的 context 供 guest export 调用。
func (inst *instance) callCtx(ctx context.Context) context.Context {
	return withState(ctx, inst.state)
}

// initInstance 完整初始化一个 wasm 实例:
// _start/_initialize → proxy_on_vm_start → context_create(plugin) → proxy_on_configure。
func initInstance(ctx context.Context, mod api.Module, vmConfig, pluginConfig []byte, logLevel LogLevel, props map[string][]byte, sd *sharedDataStore, ms *metricStore, sq *SharedQueue, ff *ForeignFuncRegistry, disp Dispatcher) (*instance, error) {
	state := newPluginState(vmConfig, pluginConfig, logLevel, props, sd, ms, sq, ff, disp)
	inst := &instance{mod: mod, state: state}
	callCtx := inst.callCtx(ctx)

	// Root context
	rootID := state.allocContextID()
	state.rootContextID = rootID
	state.activeContextID = rootID
	state.contexts[rootID] = &contextState{parentID: 0}

	// proxy_on_vm_start(root_context_id, vm_config_size)
	if fn := mod.ExportedFunction("proxy_on_vm_start"); fn != nil {
		res, err := fn.Call(callCtx, uint64(rootID), uint64(len(vmConfig)))
		if err != nil {
			return nil, fmt.Errorf("proxy_on_vm_start: %w", err)
		}
		if len(res) > 0 && res[0] == 0 {
			return nil, fmt.Errorf("proxy_on_vm_start returned false")
		}
	}

	// Plugin context
	pluginID := state.allocContextID()
	state.pluginContextID = pluginID
	state.contexts[pluginID] = &contextState{parentID: rootID}
	state.activeContextID = pluginID

	if fn := mod.ExportedFunction("proxy_on_context_create"); fn != nil {
		if _, err := fn.Call(callCtx, uint64(pluginID), uint64(rootID)); err != nil {
			return nil, fmt.Errorf("proxy_on_context_create(plugin): %w", err)
		}
	}

	// proxy_on_configure(plugin_context_id, plugin_config_size)
	if fn := mod.ExportedFunction("proxy_on_configure"); fn != nil {
		res, err := fn.Call(callCtx, uint64(pluginID), uint64(len(pluginConfig)))
		if err != nil {
			return nil, fmt.Errorf("proxy_on_configure: %w", err)
		}
		if len(res) > 0 && res[0] == 0 {
			return nil, fmt.Errorf("proxy_on_configure returned false")
		}
	}

	return inst, nil
}

// handleHTTP 在此实例上处理一个 HTTP 请求的请求阶段。
// 返回: localResponse(短路)、修改后的 headers、修改后的 body、streamID、error。
func (inst *instance) handleHTTP(ctx context.Context, r *http.Request, reqBody []byte) (*localResp, http.Header, []byte, uint32, error) {
	state := inst.state
	mod := inst.mod
	callCtx := inst.callCtx(ctx)

	// 触发待处理的 tick 回调
	inst.firePendingTicks(ctx)

	// 创建 stream context
	streamID := state.allocContextID()
	cs := &contextState{
		parentID:       state.pluginContextID,
		requestHeaders: r.Header.Clone(),
		requestBody:    reqBody,
		requestPath:    r.URL.Path,
		requestMethod:  r.Method,
	}
	if cs.requestHeaders == nil {
		cs.requestHeaders = make(http.Header)
	}
	cs.requestHeaders.Set(":method", r.Method)
	cs.requestHeaders.Set(":path", r.URL.RequestURI())
	cs.requestHeaders.Set(":authority", r.Host)
	if r.TLS != nil {
		cs.requestHeaders.Set(":scheme", "https")
	} else {
		cs.requestHeaders.Set(":scheme", "http")
	}

	state.contexts[streamID] = cs
	state.activeContextID = streamID

	// proxy_on_context_create(stream_id, plugin_id)
	if fn := mod.ExportedFunction("proxy_on_context_create"); fn != nil {
		if _, err := fn.Call(callCtx, uint64(streamID), uint64(state.pluginContextID)); err != nil {
			inst.deleteContext(ctx, streamID)
			return nil, nil, nil, 0, fmt.Errorf("proxy_on_context_create(stream): %w", err)
		}
	}

	// proxy_on_request_headers(stream_id, num_headers, end_of_stream)
	endOfStream := uint64(0)
	if len(reqBody) == 0 {
		endOfStream = 1
	}
	numHeaders := uint64(len(cs.requestHeaders))

	action := ActionContinue
	if fn := mod.ExportedFunction("proxy_on_request_headers"); fn != nil {
		state.activeContextID = streamID
		res, err := fn.Call(callCtx, uint64(streamID), numHeaders, endOfStream)
		if err != nil {
			inst.deleteContext(ctx, streamID)
			return nil, nil, nil, 0, fmt.Errorf("proxy_on_request_headers: %w", err)
		}
		if len(res) > 0 {
			action = Action(res[0])
		}
	}

	// 分发回调期间 guest 发起的异步操作
	inst.dispatchPendingCallouts(ctx, streamID)

	if cs.localResponse != nil {
		lr := cs.localResponse
		inst.deleteContext(ctx, streamID)
		return lr, nil, nil, 0, nil
	}

	// proxy_on_request_body
	if action == ActionContinue && len(reqBody) > 0 {
		if fn := mod.ExportedFunction("proxy_on_request_body"); fn != nil {
			state.activeContextID = streamID
			res, err := fn.Call(callCtx, uint64(streamID), uint64(len(reqBody)), 1)
			if err != nil {
				inst.deleteContext(ctx, streamID)
				return nil, nil, nil, 0, fmt.Errorf("proxy_on_request_body: %w", err)
			}
			if len(res) > 0 {
				action = Action(res[0])
			}
			_ = action
		}
		inst.dispatchPendingCallouts(ctx, streamID)
		if cs.localResponse != nil {
			lr := cs.localResponse
			inst.deleteContext(ctx, streamID)
			return lr, nil, nil, 0, nil
		}
	}

	// proxy_on_request_trailers (HTTP/1.1 trailers 或 gRPC trailers)
	if action == ActionContinue && r.Trailer != nil && len(r.Trailer) > 0 {
		cs.requestTrailers = r.Trailer.Clone()
		if fn := mod.ExportedFunction("proxy_on_request_trailers"); fn != nil {
			state.activeContextID = streamID
			_, err := fn.Call(callCtx, uint64(streamID), uint64(len(cs.requestTrailers)))
			if err != nil {
				inst.deleteContext(ctx, streamID)
				return nil, nil, nil, 0, fmt.Errorf("proxy_on_request_trailers: %w", err)
			}
		}
		if cs.localResponse != nil {
			lr := cs.localResponse
			inst.deleteContext(ctx, streamID)
			return lr, nil, nil, 0, nil
		}
	}

	return nil, cs.requestHeaders, cs.requestBody, streamID, nil
}

// handleResponse 处理响应阶段的 Proxy-Wasm 回调。
func (inst *instance) handleResponse(ctx context.Context, streamID uint32, status int, headers http.Header, body []byte) (*localResp, http.Header, []byte, error) {
	state := inst.state
	mod := inst.mod
	callCtx := inst.callCtx(ctx)

	cs := state.getContext(streamID)
	if cs == nil {
		return nil, headers, body, nil
	}

	cs.responseStatus = status
	cs.responseHeaders = headers.Clone()
	if cs.responseHeaders == nil {
		cs.responseHeaders = make(http.Header)
	}
	cs.responseHeaders.Set(":status", fmt.Sprintf("%d", status))
	cs.responseBody = body
	state.activeContextID = streamID

	// proxy_on_response_headers
	if fn := mod.ExportedFunction("proxy_on_response_headers"); fn != nil {
		numHeaders := uint64(len(cs.responseHeaders))
		endOfStream := uint64(0)
		if len(body) == 0 {
			endOfStream = 1
		}
		_, err := fn.Call(callCtx, uint64(streamID), numHeaders, endOfStream)
		if err != nil {
			return nil, headers, body, fmt.Errorf("proxy_on_response_headers: %w", err)
		}
	}

	if cs.localResponse != nil {
		return cs.localResponse, nil, nil, nil
	}

	// proxy_on_response_body
	if len(body) > 0 {
		if fn := mod.ExportedFunction("proxy_on_response_body"); fn != nil {
			state.activeContextID = streamID
			_, err := fn.Call(callCtx, uint64(streamID), uint64(len(cs.responseBody)), 1)
			if err != nil {
				return nil, cs.responseHeaders, cs.responseBody, fmt.Errorf("proxy_on_response_body: %w", err)
			}
		}
	}

	if cs.localResponse != nil {
		return cs.localResponse, nil, nil, nil
	}

	// proxy_on_response_trailers (通常只在 gRPC 场景出现)
	if cs.responseTrailers != nil && len(cs.responseTrailers) > 0 {
		if fn := mod.ExportedFunction("proxy_on_response_trailers"); fn != nil {
			state.activeContextID = streamID
			_, err := fn.Call(callCtx, uint64(streamID), uint64(len(cs.responseTrailers)))
			if err != nil {
				return nil, cs.responseHeaders, cs.responseBody, fmt.Errorf("proxy_on_response_trailers: %w", err)
			}
		}
		if cs.localResponse != nil {
			return cs.localResponse, nil, nil, nil
		}
	}

	// 清除伪头,避免写入实际 HTTP 响应
	finalHeaders := cs.responseHeaders.Clone()
	finalHeaders.Del(":status")

	return nil, finalHeaders, cs.responseBody, nil
}

// finishStream 调用 log + done + delete,清理 stream context。
func (inst *instance) finishStream(ctx context.Context, streamID uint32) {
	state := inst.state
	mod := inst.mod
	callCtx := inst.callCtx(ctx)
	state.activeContextID = streamID

	if fn := mod.ExportedFunction("proxy_on_log"); fn != nil {
		_, _ = fn.Call(callCtx, uint64(streamID))
	}

	if fn := mod.ExportedFunction("proxy_on_done"); fn != nil {
		_, _ = fn.Call(callCtx, uint64(streamID))
	}

	inst.deleteContext(ctx, streamID)
}

func (inst *instance) deleteContext(ctx context.Context, id uint32) {
	callCtx := inst.callCtx(ctx)
	if fn := inst.mod.ExportedFunction("proxy_on_delete"); fn != nil {
		inst.state.activeContextID = id
		_, _ = fn.Call(callCtx, uint64(id))
	}
	delete(inst.state.contexts, id)
}

// dispatchPendingCallouts 分发 guest 在刚结束的回调中发起的所有异步操作结果。
// 对于 HTTP callout: 调用 proxy_on_http_call_response(context_id, token, num_headers, body_size, num_trailers)
// 对于 gRPC callout: 调用 proxy_on_grpc_receive_initial_metadata → proxy_on_grpc_receive → proxy_on_grpc_close
func (inst *instance) dispatchPendingCallouts(ctx context.Context, streamID uint32) {
	state := inst.state
	if len(state.pendingCallouts) == 0 {
		return
	}

	callouts := state.pendingCallouts
	state.pendingCallouts = nil
	mod := inst.mod
	callCtx := inst.callCtx(ctx)

	for _, c := range callouts {
		state.activeContextID = c.contextID

		switch c.typ {
		case calloutHTTP:
			cs := state.getContext(c.contextID)
			numHeaders := uint64(0)
			bodySize := uint64(0)
			numTrailers := uint64(0)

			if c.httpResp != nil && cs != nil {
				cs.responseHeaders = c.httpResp.Headers
				cs.responseBody = c.httpResp.Body
				cs.responseTrailers = c.httpResp.Trailers
				numHeaders = uint64(len(c.httpResp.Headers))
				bodySize = uint64(len(c.httpResp.Body))
				if c.httpResp.Trailers != nil {
					numTrailers = uint64(len(c.httpResp.Trailers))
				}
			}

			if fn := mod.ExportedFunction("proxy_on_http_call_response"); fn != nil {
				_, _ = fn.Call(callCtx, uint64(c.contextID), uint64(c.token), numHeaders, bodySize, numTrailers)
			}

		case calloutGRPC:
			if c.grpcResp != nil {
				cs := state.getContext(c.contextID)
				if cs != nil {
					cs.responseHeaders = c.grpcResp.InitialMeta
					cs.responseBody = c.grpcResp.Message
					cs.responseTrailers = c.grpcResp.TrailingMeta
				}

				if fn := mod.ExportedFunction("proxy_on_grpc_receive_initial_metadata"); fn != nil {
					numMeta := uint64(0)
					if c.grpcResp.InitialMeta != nil {
						numMeta = uint64(len(c.grpcResp.InitialMeta))
					}
					_, _ = fn.Call(callCtx, uint64(c.token), numMeta)
				}

				if fn := mod.ExportedFunction("proxy_on_grpc_receive"); fn != nil {
					_, _ = fn.Call(callCtx, uint64(c.token), uint64(len(c.grpcResp.Message)))
				}

				if fn := mod.ExportedFunction("proxy_on_grpc_close"); fn != nil {
					_, _ = fn.Call(callCtx, uint64(c.token), uint64(c.grpcResp.GRPCStatus))
				}
			} else {
				// 调用失败
				if fn := mod.ExportedFunction("proxy_on_grpc_close"); fn != nil {
					_, _ = fn.Call(callCtx, uint64(c.token), uint64(2)) // UNKNOWN status
				}
			}
		}
	}

	state.activeContextID = streamID
}

// allocAndWrite 调用 guest 的 proxy_on_memory_allocate 写入数据。
func (inst *instance) allocAndWrite(ctx context.Context, data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, nil
	}
	callCtx := inst.callCtx(ctx)
	allocFn := inst.mod.ExportedFunction("proxy_on_memory_allocate")
	if allocFn == nil {
		allocFn = inst.mod.ExportedFunction("malloc")
	}
	if allocFn == nil {
		return 0, fmt.Errorf("guest exports neither proxy_on_memory_allocate nor malloc")
	}
	res, err := allocFn.Call(callCtx, uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("alloc(%d): %w", len(data), err)
	}
	if len(res) == 0 {
		return 0, fmt.Errorf("alloc returned no value")
	}
	ptr := uint32(res[0])
	mem := inst.mod.Memory()
	if mem == nil {
		return 0, fmt.Errorf("no exported memory")
	}
	if !mem.Write(ptr, data) {
		return 0, fmt.Errorf("memory write out of bounds (ptr=%d, len=%d)", ptr, len(data))
	}
	return ptr, nil
}

// firePendingTicks 触发自上次 tick 以来累积的 proxy_on_tick 回调。
// 在同步模型中，我们在每次请求开始时补发遗漏的 tick（最多一次，避免占用过多请求时间）。
func (inst *instance) firePendingTicks(ctx context.Context) {
	state := inst.state
	if state.tickPeriodMs == 0 {
		return
	}
	now := time.Now().UnixMilli()
	if state.lastTickTime == 0 {
		state.lastTickTime = now
		return
	}
	elapsed := now - state.lastTickTime
	if elapsed < int64(state.tickPeriodMs) {
		return
	}
	fn := inst.mod.ExportedFunction("proxy_on_tick")
	if fn == nil {
		return
	}
	callCtx := inst.callCtx(ctx)
	prevCtxID := state.activeContextID
	state.activeContextID = state.rootContextID
	_, _ = fn.Call(callCtx, uint64(state.rootContextID))
	state.activeContextID = prevCtxID
	state.lastTickTime = now
}

func (inst *instance) close(ctx context.Context) error {
	return inst.mod.Close(ctx)
}
