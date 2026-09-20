package session

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// RequestTracker 管理 WebSocket 上的请求/响应关联。
//
// 这是一个独立的机制原语("mechanism, not policy"):不绑定任何 wire format,
// 业务自定义消息信封格式(JSON/protobuf/自定义二进制)。
// Tracker 只管理 requestID → 响应 channel 的映射。
//
// 典型用法(客户端发 RPC):
//
//	tracker := session.NewRequestTracker()
//
//	// 注册拦截器,自动匹配响应:
//	session.Accept(handler,
//	    session.WithInterceptor(tracker.Interceptor(func(data []byte) uint64 {
//	        var env struct{ ID uint64 }
//	        json.Unmarshal(data, &env)
//	        return env.ID // 返回 0 表示非响应消息
//	    })),
//	)
//
//	// 发起请求:
//	id, wait := tracker.Track(5 * time.Second)
//	s.SendJSON(map[string]any{"id": id, "route": "game.move", "data": moveData})
//	resp, err := wait()  // 阻塞等响应或超时
//
// 典型用法(服务端回复):
//
//	// 在 OnMessage 里解析 requestID,处理后原样回填:
//	s.SendJSON(map[string]any{"id": reqID, "data": result})
//
// 并发安全。
type RequestTracker struct {
	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan []byte
}

// NewRequestTracker 创建请求追踪器。
func NewRequestTracker() *RequestTracker {
	return &RequestTracker{
		pending: make(map[uint64]chan []byte),
	}
}

// ErrRequestTimeout 请求超时。
var ErrRequestTimeout = errors.New("session: request timeout")

// ErrSessionClosed 会话关闭导致请求取消。
var ErrSessionClosed = errors.New("session: session closed")

// Track 注册一个待响应的请求,返回分配的 requestID 和等待函数。
// wait() 阻塞直到收到匹配响应、超时或会话关闭。
// 调用 wait() 后 requestID 自动从 pending 中移除。
func (t *RequestTracker) Track(timeout time.Duration) (id uint64, wait func() ([]byte, error)) {
	id = t.nextID.Add(1)
	ch := make(chan []byte, 1)

	t.mu.Lock()
	t.pending[id] = ch
	t.mu.Unlock()

	wait = func() ([]byte, error) {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		defer func() {
			t.mu.Lock()
			delete(t.pending, id)
			t.mu.Unlock()
		}()

		select {
		case data := <-ch:
			return data, nil
		case <-timer.C:
			return nil, ErrRequestTimeout
		}
	}
	return
}

// Resolve 把一条响应投递给匹配的请求。id 匹配返回 true(已消费),
// id 未找到(超时/不存在)返回 false。
func (t *RequestTracker) Resolve(id uint64, data []byte) bool {
	if id == 0 {
		return false
	}
	t.mu.Lock()
	ch, ok := t.pending[id]
	if ok {
		delete(t.pending, id)
	}
	t.mu.Unlock()
	if !ok {
		return false
	}
	ch <- data
	return true
}

// Pending 返回当前待响应的请求数量。
func (t *RequestTracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// CancelAll 取消所有待响应的请求(如会话关闭时调用)。
// 所有 wait() 将收到 nil 数据(调用者应检查)。
func (t *RequestTracker) CancelAll() {
	t.mu.Lock()
	pending := t.pending
	t.pending = make(map[uint64]chan []byte)
	t.mu.Unlock()

	for _, ch := range pending {
		close(ch)
	}
}

// Interceptor 返回一个 Session 拦截器,自动匹配响应消息。
// extractID 从消息负载中提取 requestID;返回 0 表示不是响应消息(放行给 OnMessage)。
// 业务根据自己的 wire format 实现 extractID(JSON/protobuf/自定义二进制均可)。
//
// 用法:
//
//	session.Accept(handler,
//	    session.WithInterceptor(tracker.Interceptor(func(data []byte) uint64 {
//	        var env struct{ ID uint64 `json:"id"` }
//	        json.Unmarshal(data, &env)
//	        return env.ID
//	    })),
//	)
func (t *RequestTracker) Interceptor(extractID func(data []byte) uint64) Interceptor {
	return func(s *Session, typ MessageType, data []byte) (bool, error) {
		id := extractID(data)
		if id == 0 {
			return false, nil
		}
		return t.Resolve(id, data), nil
	}
}
