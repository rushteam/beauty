package proxywasm

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net/http"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// hostcalls.go 实现所有 proxy_* host function。
// 每个 host function 签名符合 wazero 要求: (ctx, mod, numeric args...) → numeric results。
// 通过 ctx.Value(ctxKey) 获取 pluginState。

// registerHostFunctions 在 wazero runtime 上注册所有 proxy_* host function。
func registerHostFunctions(b *hostModuleBuilder, logger *slog.Logger) {
	// ======== 日志 ========
	b.export("proxy_log", func(ctx context.Context, mod api.Module, level, msgPtr, msgSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		msg := readString(mod, msgPtr, msgSize)
		logLevel := LogLevel(level)
		if logger != nil && logLevel >= state.logLevel {
			switch logLevel {
			case LogTrace, LogDebug:
				logger.Debug(msg, slog.String("source", "wasm"))
			case LogInfo:
				logger.Info(msg, slog.String("source", "wasm"))
			case LogWarn:
				logger.Warn(msg, slog.String("source", "wasm"))
			case LogError, LogCritical:
				logger.Error(msg, slog.String("source", "wasm"))
			}
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_log_level", func(ctx context.Context, mod api.Module, returnLevel uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		writeUint32(mod, returnLevel, uint32(state.logLevel))
		return uint32(WasmResultOk)
	})

	// ======== 时间 ========
	b.export("proxy_get_current_time_nanoseconds", func(ctx context.Context, mod api.Module, returnTime uint32) uint32 {
		now := uint64(time.Now().UnixNano())
		mem := mod.Memory()
		if mem == nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, now)
		if !mem.Write(returnTime, buf) {
			return uint32(WasmResultInvalidMemAccess)
		}
		return uint32(WasmResultOk)
	})

	// ======== 定时器 ========
	b.export("proxy_set_tick_period_milliseconds", func(ctx context.Context, mod api.Module, period uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		state.tickPeriodMs = period
		return uint32(WasmResultOk)
	})

	// ======== Header Maps ========
	b.export("proxy_get_header_map_pairs", func(ctx context.Context, mod api.Module, mapType, returnDataPtr, returnDataSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		h := state.getHeaderMap(MapType(mapType))
		if h == nil {
			writeUint32(mod, returnDataPtr, 0)
			writeUint32(mod, returnDataSize, 0)
			return uint32(WasmResultOk)
		}
		data := encodeHeaderMap(h)
		inst := &instance{mod: mod, state: state}
		ptr, err := inst.allocAndWrite(ctx, data)
		if err != nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		writeUint32(mod, returnDataPtr, ptr)
		writeUint32(mod, returnDataSize, uint32(len(data)))
		return uint32(WasmResultOk)
	})

	b.export("proxy_set_header_map_pairs", func(ctx context.Context, mod api.Module, mapType, dataPtr, dataSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		data := readBytes(mod, dataPtr, dataSize)
		if data == nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		pairs := decodeHeaderMap(data)
		h := pairsToHeader(pairs)
		state.setHeaderMap(MapType(mapType), h)
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_header_map_value", func(ctx context.Context, mod api.Module, mapType, keyPtr, keySize, returnValuePtr, returnValueSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		h := state.getHeaderMap(MapType(mapType))
		if h == nil {
			return uint32(WasmResultNotFound)
		}
		key := readString(mod, keyPtr, keySize)
		val, found := headerGet(h, key)
		if !found {
			return uint32(WasmResultNotFound)
		}
		inst := &instance{mod: mod, state: state}
		ptr, err := inst.allocAndWrite(ctx, []byte(val))
		if err != nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		writeUint32(mod, returnValuePtr, ptr)
		writeUint32(mod, returnValueSize, uint32(len(val)))
		return uint32(WasmResultOk)
	})

	b.export("proxy_add_header_map_value", func(ctx context.Context, mod api.Module, mapType, keyPtr, keySize, valPtr, valSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		h := state.getHeaderMap(MapType(mapType))
		if h == nil {
			return uint32(WasmResultBadArgument)
		}
		key := readString(mod, keyPtr, keySize)
		val := readString(mod, valPtr, valSize)
		h.Add(key, val)
		return uint32(WasmResultOk)
	})

	b.export("proxy_replace_header_map_value", func(ctx context.Context, mod api.Module, mapType, keyPtr, keySize, valPtr, valSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		h := state.getHeaderMap(MapType(mapType))
		if h == nil {
			return uint32(WasmResultBadArgument)
		}
		key := readString(mod, keyPtr, keySize)
		val := readString(mod, valPtr, valSize)
		h.Set(key, val)
		return uint32(WasmResultOk)
	})

	b.export("proxy_remove_header_map_value", func(ctx context.Context, mod api.Module, mapType, keyPtr, keySize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		h := state.getHeaderMap(MapType(mapType))
		if h == nil {
			return uint32(WasmResultBadArgument)
		}
		key := readString(mod, keyPtr, keySize)
		h.Del(key)
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_header_map_size", func(ctx context.Context, mod api.Module, mapType, returnSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		h := state.getHeaderMap(MapType(mapType))
		if h == nil {
			writeUint32(mod, returnSize, 0)
			return uint32(WasmResultOk)
		}
		data := encodeHeaderMap(h)
		writeUint32(mod, returnSize, uint32(len(data)))
		return uint32(WasmResultOk)
	})

	// ======== Buffers ========
	b.export("proxy_get_buffer_bytes", func(ctx context.Context, mod api.Module, bufType, start, maxSize, returnDataPtr, returnDataSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		buf := state.getBuffer(BufferType(bufType))
		if buf == nil {
			writeUint32(mod, returnDataPtr, 0)
			writeUint32(mod, returnDataSize, 0)
			return uint32(WasmResultOk)
		}
		if int(start) >= len(buf) {
			writeUint32(mod, returnDataPtr, 0)
			writeUint32(mod, returnDataSize, 0)
			return uint32(WasmResultOk)
		}
		end := int(start) + int(maxSize)
		if end > len(buf) || maxSize == 0 {
			end = len(buf)
		}
		slice := buf[start:end]
		inst := &instance{mod: mod, state: state}
		ptr, err := inst.allocAndWrite(ctx, slice)
		if err != nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		writeUint32(mod, returnDataPtr, ptr)
		writeUint32(mod, returnDataSize, uint32(len(slice)))
		return uint32(WasmResultOk)
	})

	b.export("proxy_set_buffer_bytes", func(ctx context.Context, mod api.Module, bufType, start, maxSize, dataPtr, dataSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		data := readBytes(mod, dataPtr, dataSize)
		existing := state.getBuffer(BufferType(bufType))

		var newBuf []byte
		if start == 0 && (maxSize == 0 || int(maxSize) >= len(existing)) {
			newBuf = data
		} else {
			newBuf = make([]byte, 0, len(existing))
			if int(start) <= len(existing) {
				newBuf = append(newBuf, existing[:start]...)
			}
			newBuf = append(newBuf, data...)
			endOld := int(start) + int(maxSize)
			if endOld < len(existing) {
				newBuf = append(newBuf, existing[endOld:]...)
			}
		}
		if !state.setBuffer(BufferType(bufType), newBuf) {
			return uint32(WasmResultBadArgument)
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_buffer_status", func(ctx context.Context, mod api.Module, bufType, returnLengthPtr, returnFlagsPtr uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		size := state.getBufferSize(BufferType(bufType))
		writeUint32(mod, returnLengthPtr, uint32(size))
		writeUint32(mod, returnFlagsPtr, 0)
		return uint32(WasmResultOk)
	})

	// ======== 流控 ========
	b.export("proxy_send_local_response", func(ctx context.Context, mod api.Module, statusCode, statusPtr, statusSize, bodyPtr, bodySize, headersPtr, headersSize, grpcStatus uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		cs := state.activeContext()
		if cs == nil {
			return uint32(WasmResultBadArgument)
		}
		body := readBytes(mod, bodyPtr, bodySize)
		var headers http.Header
		if headersSize > 0 {
			hdata := readBytes(mod, headersPtr, headersSize)
			pairs := decodeHeaderMap(hdata)
			headers = pairsToHeader(pairs)
		}
		cs.localResponse = &localResp{
			status:  int(statusCode),
			headers: headers,
			body:    append([]byte(nil), body...),
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_continue_stream", func(ctx context.Context, mod api.Module, streamType uint32) uint32 {
		// 同步模型中 continue 即 no-op（流已在进行中）
		return uint32(WasmResultOk)
	})

	b.export("proxy_close_stream", func(ctx context.Context, mod api.Module, streamType uint32) uint32 {
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_status", func(ctx context.Context, mod api.Module, returnCodePtr uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		cs := state.activeContext()
		if cs == nil {
			return uint32(WasmResultBadArgument)
		}
		writeUint32(mod, returnCodePtr, uint32(cs.responseStatus))
		return uint32(WasmResultOk)
	})

	// ======== 上下文 ========
	b.export("proxy_set_effective_context", func(ctx context.Context, mod api.Module, contextID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		if state.getContext(contextID) == nil {
			return uint32(WasmResultBadArgument)
		}
		state.activeContextID = contextID
		return uint32(WasmResultOk)
	})

	b.export("proxy_done", func(ctx context.Context, mod api.Module) uint32 {
		return uint32(WasmResultOk)
	})

	// ======== Properties ========
	b.export("proxy_get_property", func(ctx context.Context, mod api.Module, pathPtr, pathSize, returnValuePtr, returnValueSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		path := readBytes(mod, pathPtr, pathSize)
		val, result := state.getProperty(path)
		if result != WasmResultOk {
			return uint32(result)
		}
		inst := &instance{mod: mod, state: state}
		ptr, err := inst.allocAndWrite(ctx, val)
		if err != nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		writeUint32(mod, returnValuePtr, ptr)
		writeUint32(mod, returnValueSize, uint32(len(val)))
		return uint32(WasmResultOk)
	})

	b.export("proxy_set_property", func(ctx context.Context, mod api.Module, pathPtr, pathSize, valPtr, valSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		path := readBytes(mod, pathPtr, pathSize)
		val := readBytes(mod, valPtr, valSize)
		result := state.setProperty(path, val)
		return uint32(result)
	})

	// ======== Async Operations ========
	b.export("proxy_http_call", func(ctx context.Context, mod api.Module, uriPtr, uriSize, headersPtr, headersSize, bodyPtr, bodySize, trailersPtr, trailersSize, timeout, returnCalloutID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		if state.dispatcher == nil {
			return uint32(WasmResultUnimplemented)
		}

		upstream := readString(mod, uriPtr, uriSize)
		headerData := readBytes(mod, headersPtr, headersSize)
		body := readBytes(mod, bodyPtr, bodySize)
		trailerData := readBytes(mod, trailersPtr, trailersSize)

		headers := pairsToHeader(decodeHeaderMap(headerData))
		trailers := pairsToHeader(decodeHeaderMap(trailerData))

		timeoutDur := time.Duration(timeout) * time.Millisecond

		token := state.nextCalloutID
		state.nextCalloutID++

		resp, err := state.dispatcher.HTTPCall(ctx, &HTTPCallRequest{
			Upstream: upstream,
			Headers:  headers,
			Body:     body,
			Trailers: trailers,
			Timeout:  timeoutDur,
		})

		state.pendingCallouts = append(state.pendingCallouts, &pendingCallout{
			typ:       calloutHTTP,
			token:     token,
			contextID: state.activeContextID,
			httpResp:  resp,
			err:       err,
		})

		writeUint32(mod, returnCalloutID, token)
		return uint32(WasmResultOk)
	})

	b.export("proxy_grpc_call", func(ctx context.Context, mod api.Module, servicePtr, serviceSize, serviceNamePtr, serviceNameSize, methodPtr, methodSize, initialMetaPtr, initialMetaSize, msgPtr, msgSize, timeout, returnCalloutID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		if state.dispatcher == nil {
			return uint32(WasmResultUnimplemented)
		}

		service := readString(mod, servicePtr, serviceSize)
		serviceName := readString(mod, serviceNamePtr, serviceNameSize)
		method := readString(mod, methodPtr, methodSize)
		metaData := readBytes(mod, initialMetaPtr, initialMetaSize)
		msg := readBytes(mod, msgPtr, msgSize)

		initialMeta := pairsToHeader(decodeHeaderMap(metaData))
		timeoutDur := time.Duration(timeout) * time.Millisecond

		token := state.nextCalloutID
		state.nextCalloutID++

		resp, err := state.dispatcher.GRPCCall(ctx, &GRPCCallRequest{
			Service:     service,
			ServiceName: serviceName,
			Method:      method,
			InitialMeta: initialMeta,
			Message:     msg,
			Timeout:     timeoutDur,
		})

		state.pendingCallouts = append(state.pendingCallouts, &pendingCallout{
			typ:       calloutGRPC,
			token:     token,
			contextID: state.activeContextID,
			grpcResp:  resp,
			err:       err,
		})

		writeUint32(mod, returnCalloutID, token)
		return uint32(WasmResultOk)
	})

	b.export("proxy_grpc_stream", func(ctx context.Context, mod api.Module, servicePtr, serviceSize, serviceNamePtr, serviceNameSize, initialMetaPtr, initialMetaSize, returnStreamID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		service := readString(mod, servicePtr, serviceSize)
		serviceName := readString(mod, serviceNamePtr, serviceNameSize)

		id := state.nextStreamID
		state.nextStreamID++
		state.grpcStreams[id] = &grpcStreamState{
			id:          id,
			service:     service,
			serviceName: serviceName,
		}
		writeUint32(mod, returnStreamID, id)
		return uint32(WasmResultOk)
	})

	b.export("proxy_grpc_send", func(ctx context.Context, mod api.Module, streamID, msgPtr, msgSize, endOfStream uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		s, ok := state.grpcStreams[streamID]
		if !ok || s.closed {
			return uint32(WasmResultBadArgument)
		}
		if endOfStream == 1 {
			s.closed = true
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_grpc_cancel", func(ctx context.Context, mod api.Module, streamID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		s, ok := state.grpcStreams[streamID]
		if !ok {
			return uint32(WasmResultBadArgument)
		}
		s.closed = true
		return uint32(WasmResultOk)
	})

	b.export("proxy_grpc_close", func(ctx context.Context, mod api.Module, streamID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil {
			return uint32(WasmResultInternalFailure)
		}
		s, ok := state.grpcStreams[streamID]
		if !ok {
			return uint32(WasmResultBadArgument)
		}
		s.closed = true
		delete(state.grpcStreams, streamID)
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_shared_data", func(ctx context.Context, mod api.Module, keyPtr, keySize, returnValPtr, returnValSize, returnCAS uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.sharedData == nil {
			return uint32(WasmResultInternalFailure)
		}
		key := readString(mod, keyPtr, keySize)
		data, cas, ok := state.sharedData.Get(key)
		if !ok {
			return uint32(WasmResultNotFound)
		}
		inst := &instance{mod: mod, state: state}
		ptr, err := inst.allocAndWrite(ctx, data)
		if err != nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		writeUint32(mod, returnValPtr, ptr)
		writeUint32(mod, returnValSize, uint32(len(data)))
		writeUint32(mod, returnCAS, cas)
		return uint32(WasmResultOk)
	})

	b.export("proxy_set_shared_data", func(ctx context.Context, mod api.Module, keyPtr, keySize, valPtr, valSize, cas uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.sharedData == nil {
			return uint32(WasmResultInternalFailure)
		}
		key := readString(mod, keyPtr, keySize)
		val := readBytes(mod, valPtr, valSize)
		if !state.sharedData.Set(key, val, cas) {
			return uint32(WasmResultCASMismatch)
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_register_shared_queue", func(ctx context.Context, mod api.Module, namePtr, nameSize, returnID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.sharedQueue == nil {
			return uint32(WasmResultInternalFailure)
		}
		name := readString(mod, namePtr, nameSize)
		id := state.sharedQueue.Register("", name)
		writeUint32(mod, returnID, id)
		return uint32(WasmResultOk)
	})

	b.export("proxy_resolve_shared_queue", func(ctx context.Context, mod api.Module, vmIDPtr, vmIDSize, namePtr, nameSize, returnID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.sharedQueue == nil {
			return uint32(WasmResultInternalFailure)
		}
		vmID := readString(mod, vmIDPtr, vmIDSize)
		name := readString(mod, namePtr, nameSize)
		id, ok := state.sharedQueue.Resolve(vmID, name)
		if !ok {
			return uint32(WasmResultNotFound)
		}
		writeUint32(mod, returnID, id)
		return uint32(WasmResultOk)
	})

	b.export("proxy_enqueue_shared_queue", func(ctx context.Context, mod api.Module, queueID, dataPtr, dataSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.sharedQueue == nil {
			return uint32(WasmResultInternalFailure)
		}
		data := readBytes(mod, dataPtr, dataSize)
		if !state.sharedQueue.Enqueue(queueID, data) {
			return uint32(WasmResultNotFound)
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_dequeue_shared_queue", func(ctx context.Context, mod api.Module, queueID, returnDataPtr, returnDataSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.sharedQueue == nil {
			return uint32(WasmResultInternalFailure)
		}
		data, ok := state.sharedQueue.Dequeue(queueID)
		if !ok {
			return uint32(WasmResultEmpty)
		}
		inst := &instance{mod: mod, state: state}
		ptr, err := inst.allocAndWrite(ctx, data)
		if err != nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		writeUint32(mod, returnDataPtr, ptr)
		writeUint32(mod, returnDataSize, uint32(len(data)))
		return uint32(WasmResultOk)
	})

	b.export("proxy_define_metric", func(ctx context.Context, mod api.Module, metricType, namePtr, nameSize, returnID uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.metrics == nil {
			return uint32(WasmResultInternalFailure)
		}
		name := readString(mod, namePtr, nameSize)
		id := state.metrics.Define(metricType, name)
		writeUint32(mod, returnID, id)
		return uint32(WasmResultOk)
	})

	b.export("proxy_record_metric", func(ctx context.Context, mod api.Module, metricID uint32, value uint64) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.metrics == nil {
			return uint32(WasmResultInternalFailure)
		}
		if !state.metrics.Record(metricID, value) {
			return uint32(WasmResultNotFound)
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_increment_metric", func(ctx context.Context, mod api.Module, metricID uint32, offset int64) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.metrics == nil {
			return uint32(WasmResultInternalFailure)
		}
		if !state.metrics.Increment(metricID, offset) {
			return uint32(WasmResultNotFound)
		}
		return uint32(WasmResultOk)
	})

	b.export("proxy_get_metric", func(ctx context.Context, mod api.Module, metricID, returnValue uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.metrics == nil {
			return uint32(WasmResultInternalFailure)
		}
		val, ok := state.metrics.Get(metricID)
		if !ok {
			return uint32(WasmResultNotFound)
		}
		mem := mod.Memory()
		if mem == nil {
			return uint32(WasmResultInvalidMemAccess)
		}
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, val)
		mem.Write(returnValue, buf)
		return uint32(WasmResultOk)
	})

	b.export("proxy_call_foreign_function", func(ctx context.Context, mod api.Module, funcNamePtr, funcNameSize, argsPtr, argsSize, returnResultPtr, returnResultSize uint32) uint32 {
		state := stateFromCtx(ctx)
		if state == nil || state.foreignFuncs == nil {
			return uint32(WasmResultInternalFailure)
		}
		name := readString(mod, funcNamePtr, funcNameSize)
		args := readBytes(mod, argsPtr, argsSize)
		result, err := state.foreignFuncs.Call(name, args)
		if err != nil {
			return uint32(WasmResultNotFound)
		}
		if len(result) > 0 {
			inst := &instance{mod: mod, state: state}
			ptr, allocErr := inst.allocAndWrite(ctx, result)
			if allocErr != nil {
				return uint32(WasmResultInvalidMemAccess)
			}
			writeUint32(mod, returnResultPtr, ptr)
			writeUint32(mod, returnResultSize, uint32(len(result)))
		} else {
			writeUint32(mod, returnResultPtr, 0)
			writeUint32(mod, returnResultSize, 0)
		}
		return uint32(WasmResultOk)
	})
}

// getHeaderMap 返回当前活跃 context 中指定类型的 header map。
func (ps *pluginState) getHeaderMap(mt MapType) http.Header {
	cs := ps.activeContext()
	if cs == nil {
		return nil
	}
	switch mt {
	case MapTypeHTTPRequestHeaders:
		return cs.requestHeaders
	case MapTypeHTTPRequestTrailers:
		return cs.requestTrailers
	case MapTypeHTTPResponseHeaders:
		return cs.responseHeaders
	case MapTypeHTTPResponseTrailers:
		return cs.responseTrailers
	}
	return nil
}

// setHeaderMap 设置指定类型的 header map。
func (ps *pluginState) setHeaderMap(mt MapType, h http.Header) {
	cs := ps.activeContext()
	if cs == nil {
		return
	}
	switch mt {
	case MapTypeHTTPRequestHeaders:
		cs.requestHeaders = h
	case MapTypeHTTPRequestTrailers:
		cs.requestTrailers = h
	case MapTypeHTTPResponseHeaders:
		cs.responseHeaders = h
	case MapTypeHTTPResponseTrailers:
		cs.responseTrailers = h
	}
}

// ---- 内存辅助 ----

func readBytes(mod api.Module, ptr, size uint32) []byte {
	if size == 0 {
		return nil
	}
	mem := mod.Memory()
	if mem == nil {
		return nil
	}
	data, ok := mem.Read(ptr, size)
	if !ok {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func readString(mod api.Module, ptr, size uint32) string {
	b := readBytes(mod, ptr, size)
	return string(b)
}

func writeUint32(mod api.Module, ptr, val uint32) {
	mem := mod.Memory()
	if mem == nil {
		return
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, val)
	mem.Write(ptr, buf)
}

// hostModuleBuilder 抽象 wazero 的 host module builder,便于注册函数。
type hostModuleBuilder struct {
	fns map[string]any
}

func newHostModuleBuilder() *hostModuleBuilder {
	return &hostModuleBuilder{fns: make(map[string]any)}
}

func (b *hostModuleBuilder) export(name string, fn any) {
	b.fns[name] = fn
}
