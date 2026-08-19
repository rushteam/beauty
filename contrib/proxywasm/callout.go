package proxywasm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ---- Async Operation Abstraction ----
//
// Proxy-Wasm 定义了多种异步操作(HTTP callout, gRPC call/stream, shared queue)。
// 在宿主侧(Envoy/Higress)这些是真正的异步: 注册→挂起→回调恢复。
//
// 在 Beauty 的同步 net/http 模型中, 我们将异步操作转化为:
//   1. Guest 调用 proxy_http_call 等 → host 同步执行(阻塞当前 goroutine)
//   2. 结果暂存到 pendingCallouts 队列
//   3. 当前 guest 回调返回后, host 依次分发 proxy_on_http_call_response 等回调
//   4. 分发完成后继续正常请求处理流程
//
// 这样做的好处: 无额外 goroutine, 无竞态, 模型简单。
// 代价: 外部调用延迟会阻塞请求处理(可通过 timeout 控制)。

// Dispatcher 处理 guest 发起的异步操作。
// 用户可通过 FilterOption 注入自定义 Dispatcher 来对接自己的基础设施。
type Dispatcher interface {
	// HTTPCall 执行 HTTP 外发请求(同步阻塞)。
	HTTPCall(ctx context.Context, req *HTTPCallRequest) (*HTTPCallResponse, error)

	// GRPCCall 执行 gRPC unary 调用(同步阻塞)。
	GRPCCall(ctx context.Context, req *GRPCCallRequest) (*GRPCCallResponse, error)
}

// HTTPCallRequest 是 proxy_http_call 的参数。
type HTTPCallRequest struct {
	Upstream string
	Headers  http.Header
	Body     []byte
	Trailers http.Header
	Timeout  time.Duration
}

// HTTPCallResponse 是 HTTP callout 的响应。
type HTTPCallResponse struct {
	Headers  http.Header
	Body     []byte
	Trailers http.Header
}

// GRPCCallRequest 是 proxy_grpc_call 的参数。
type GRPCCallRequest struct {
	Service     string
	ServiceName string
	Method      string
	InitialMeta http.Header
	Message     []byte
	Timeout     time.Duration
}

// GRPCCallResponse 是 gRPC unary 调用的响应。
type GRPCCallResponse struct {
	InitialMeta  http.Header
	Message      []byte
	TrailingMeta http.Header
	GRPCStatus   uint32
}

// ---- Pending Callout ----

// calloutType 标识 pending callout 的类型。
type calloutType int

const (
	calloutHTTP calloutType = iota
	calloutGRPC
)

// pendingCallout 是一个等待分发回调的异步操作结果。
type pendingCallout struct {
	typ       calloutType
	token     uint32
	contextID uint32 // 发起调用的 stream context ID

	httpResp *HTTPCallResponse
	grpcResp *GRPCCallResponse
	err      error
}

// ---- gRPC Stream State ----
// gRPC 双向流需要更复杂的状态管理,当前仅实现 unary 语义(send→recv→close)。

type grpcStreamState struct {
	id          uint32
	service     string
	serviceName string
	closed      bool
}

// ---- Default Dispatcher ----

// DefaultDispatcher 使用标准 net/http.Client 执行 HTTP callout。
// gRPC 默认不支持(返回 error), 用户需注入自定义 Dispatcher。
type DefaultDispatcher struct {
	Client *http.Client
}

func (d *DefaultDispatcher) HTTPCall(ctx context.Context, req *HTTPCallRequest) (*HTTPCallResponse, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{}
	}

	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	method := "GET"
	if req.Headers != nil {
		if m := req.Headers.Get(":method"); m != "" {
			method = m
		}
	}

	path := "/"
	if req.Headers != nil {
		if p := req.Headers.Get(":path"); p != "" {
			path = p
		}
	}

	scheme := "http"
	if req.Headers != nil {
		if s := req.Headers.Get(":scheme"); s != "" {
			scheme = s
		}
	}

	authority := req.Upstream
	if req.Headers != nil {
		if a := req.Headers.Get(":authority"); a != "" {
			authority = a
		}
	}

	url := fmt.Sprintf("%s://%s%s", scheme, authority, path)

	var bodyReader *bytesReader
	if len(req.Body) > 0 {
		bodyReader = &bytesReader{data: req.Body}
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, vs := range req.Headers {
		if len(k) > 0 && k[0] == ':' {
			continue // skip pseudo-headers
		}
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	body, err := readAll(resp.Body, 10<<20) // 10MB max
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	respHeaders := resp.Header.Clone()
	respHeaders.Set(":status", fmt.Sprintf("%d", resp.StatusCode))

	return &HTTPCallResponse{
		Headers:  respHeaders,
		Body:     body,
		Trailers: resp.Trailer,
	}, nil
}

func (d *DefaultDispatcher) GRPCCall(ctx context.Context, req *GRPCCallRequest) (*GRPCCallResponse, error) {
	return nil, fmt.Errorf("gRPC call not supported by DefaultDispatcher; inject a custom Dispatcher")
}

// ---- Foreign Function ----

// ForeignFunc 是用户注册的自定义扩展函数。
type ForeignFunc func(args []byte) ([]byte, error)

// ForeignFuncRegistry 管理已注册的 foreign functions。
type ForeignFuncRegistry struct {
	funcs map[string]ForeignFunc
}

func newForeignFuncRegistry() *ForeignFuncRegistry {
	return &ForeignFuncRegistry{funcs: make(map[string]ForeignFunc)}
}

// Register 注册一个 foreign function。
func (r *ForeignFuncRegistry) Register(name string, fn ForeignFunc) {
	r.funcs[name] = fn
}

// Call 调用已注册的 foreign function。
func (r *ForeignFuncRegistry) Call(name string, args []byte) ([]byte, error) {
	fn, ok := r.funcs[name]
	if !ok {
		return nil, fmt.Errorf("foreign function %q not registered", name)
	}
	return fn(args)
}

// ---- helpers ----

type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func readAll(r interface{ Read([]byte) (int, error) }, maxBytes int) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) > maxBytes {
				return buf[:maxBytes], nil
			}
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}
