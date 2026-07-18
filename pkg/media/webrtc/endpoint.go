package webrtc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"

	pion "github.com/pion/webrtc/v4"
)

// NegotiateFunc 在收到一次 WHIP/WHEP offer 时被调用:streamKey 取自请求路径(挂载点
// 之后的部分),pc 是本次会话新建的 PeerConnection。在此:
//   - 做鉴权/准入:返回非 nil error 即拒绝(关闭 pc,回 403);
//   - WHIP(收流):注册 pc.OnTrack 拿进来的轨道;
//   - WHEP(发流):pc.AddTrack 把要发送的本地轨道挂上。
//
// 返回 nil 后本包生成 answer 并回 201。回调应尽快返回(不要在里面阻塞读 RTP——
// 用 OnTrack 回调里另起 goroutine)。
type NegotiateFunc func(streamKey string, pc *pion.PeerConnection) error

// Endpoint 是 WHIP/WHEP 的服务端 http.Handler:
//   - POST(application/sdp):协商一路会话,回 201 + answer + Location(资源地址);
//   - DELETE <Location>:拆除该会话;
//   - 断连(ICE Failed/Closed)自动回收。
//
// 挂载到任意路由前缀即可,streamKey = 前缀之后的路径。例如
// mux.Handle("/whip/", http.StripPrefix("/whip/", ep)) 则 POST /whip/room1 的 streamKey="room1"。
// 零值不可用,用 NewEndpoint / NewWHIP / NewWHEP 构造。
type Endpoint struct {
	api       *pion.API
	config    pion.Configuration
	negotiate NegotiateFunc
	name      string
	basePath  string // 挂载前缀,如 "/whip";用于剥出 streamKey 并生成绝对 Location

	mu       sync.Mutex
	sessions map[string]*pion.PeerConnection
}

// EndpointOption 配置 Endpoint。
type EndpointOption func(*Endpoint)

// WithICEServers 设置 STUN/TURN 服务器(NAT 穿透)。不设则只用主机候选(仅同网段/本机可达)。
func WithICEServers(servers ...pion.ICEServer) EndpointOption {
	return func(e *Endpoint) { e.config.ICEServers = append(e.config.ICEServers, servers...) }
}

// WithConfiguration 直接覆盖 PeerConnection 配置(ICEServers、ICETransportPolicy 等)。
func WithConfiguration(c pion.Configuration) EndpointOption {
	return func(e *Endpoint) { e.config = c }
}

// WithEndpointName 设置名字(日志标识用)。
func WithEndpointName(name string) EndpointOption {
	return func(e *Endpoint) {
		if name != "" {
			e.name = name
		}
	}
}

// WithBasePath 告知 Endpoint 自己的挂载前缀(如 "/whip"),这样:
//   - streamKey = 请求路径去掉该前缀后的部分;
//   - Location 头是绝对路径(<basePath>/<streamKey>/<id>),DELETE 能直接路由回来。
//
// 用法:mux.Handle("/whip/", NewWHIP(api, fn, WithBasePath("/whip")))。不设时按相对
// 路径处理(适配 http.StripPrefix 挂载,但 Location 为相对引用)。
func WithBasePath(p string) EndpointOption {
	return func(e *Endpoint) { e.basePath = "/" + strings.Trim(p, "/") }
}

// NewEndpoint 构造一个 WHIP/WHEP 端点。api 由 NewAPI 得到;negotiate 决定每路会话怎么接。
func NewEndpoint(api *pion.API, negotiate NegotiateFunc, opts ...EndpointOption) *Endpoint {
	e := &Endpoint{
		api:       api,
		negotiate: negotiate,
		name:      "webrtc",
		sessions:  make(map[string]*pion.PeerConnection),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// NewWHIP 构造一个 WHIP(采集)端点——语义上「收流」,请在 onPublish 里注册 pc.OnTrack。
// 与 NewEndpoint 机制相同,仅默认名字不同,便于识别。
func NewWHIP(api *pion.API, onPublish NegotiateFunc, opts ...EndpointOption) *Endpoint {
	return NewEndpoint(api, onPublish, append([]EndpointOption{WithEndpointName("whip")}, opts...)...)
}

// NewWHEP 构造一个 WHEP(分发)端点——语义上「发流」,请在 onSubscribe 里 pc.AddTrack。
// 与 NewEndpoint 机制相同,仅默认名字不同,便于识别。
func NewWHEP(api *pion.API, onSubscribe NegotiateFunc, opts ...EndpointOption) *Endpoint {
	return NewEndpoint(api, onSubscribe, append([]EndpointOption{WithEndpointName("whep")}, opts...)...)
}

// ServeHTTP 实现 http.Handler。
func (e *Endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		e.create(w, r)
	case http.MethodDelete:
		e.destroy(w, r)
	case http.MethodOptions:
		// 预检放行;具体 CORS 头是 policy,交给上层中间件补。
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "POST, DELETE, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (e *Endpoint) create(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/sdp") {
		http.Error(w, "expected Content-Type: application/sdp", http.StatusUnsupportedMediaType)
		return
	}
	offer, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // SDP 不该超过 1MB
	if err != nil {
		http.Error(w, "read offer: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(offer) == 0 {
		http.Error(w, "empty SDP offer", http.StatusBadRequest)
		return
	}
	streamKey := strings.Trim(strings.TrimPrefix(r.URL.Path, e.basePath), "/")

	pc, err := e.api.NewPeerConnection(e.config)
	if err != nil {
		http.Error(w, "new peerconnection: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := e.negotiate(streamKey, pc); err != nil {
		_ = pc.Close()
		http.Error(w, "rejected: "+err.Error(), http.StatusForbidden)
		return
	}
	answer, err := Answer(pc, string(offer))
	if err != nil {
		_ = pc.Close()
		http.Error(w, "negotiate: "+err.Error(), http.StatusInternalServerError)
		return
	}
	id, err := randomID()
	if err != nil {
		_ = pc.Close()
		http.Error(w, "id: "+err.Error(), http.StatusInternalServerError)
		return
	}

	e.mu.Lock()
	e.sessions[id] = pc
	e.mu.Unlock()

	// ICE 失败/关闭时自动回收(即使客户端没发 DELETE 也不泄漏)。
	pc.OnConnectionStateChange(func(s pion.PeerConnectionState) {
		switch s {
		case pion.PeerConnectionStateFailed, pion.PeerConnectionStateClosed:
			e.remove(id)
		}
	})

	// Location 指向本次会话资源(相对当前请求路径 + id);DELETE 到此即拆除。
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", strings.TrimRight(r.URL.Path, "/")+"/"+id)
	w.WriteHeader(http.StatusCreated)
	if _, err := io.WriteString(w, answer); err != nil {
		slog.Debug("webrtc: write answer failed", "endpoint", e.name, "err", err)
	}
}

func (e *Endpoint) destroy(w http.ResponseWriter, r *http.Request) {
	id := path.Base(strings.TrimRight(r.URL.Path, "/"))
	if e.remove(id) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Error(w, "no such session", http.StatusNotFound)
}

// remove 关闭并注销一路会话;返回是否存在。幂等(OnConnectionStateChange 的 Closed
// 回调会再次触发 remove,第二次为 no-op)。
func (e *Endpoint) remove(id string) bool {
	e.mu.Lock()
	pc, ok := e.sessions[id]
	delete(e.sessions, id)
	e.mu.Unlock()
	if ok && pc != nil {
		_ = pc.Close()
	}
	return ok
}

// Count 返回当前活跃会话数。
func (e *Endpoint) Count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sessions)
}

// Close 关闭所有活跃会话(用于优雅停机)。
func (e *Endpoint) Close() error {
	e.mu.Lock()
	pcs := make([]*pion.PeerConnection, 0, len(e.sessions))
	for id, pc := range e.sessions {
		pcs = append(pcs, pc)
		delete(e.sessions, id)
	}
	e.mu.Unlock()
	for _, pc := range pcs {
		_ = pc.Close()
	}
	return nil
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webrtc: random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
