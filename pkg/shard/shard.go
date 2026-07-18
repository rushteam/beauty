// Package shard 给「有状态服务」提供多副本分片路由的薄机制:用一致性哈希把每个 key
// (streamKey / roomID / userID 等)确定性地归属到某一个实例,请求落到非归属实例时
// 反向代理转发给归属实例。从而让 media.Hub、webrtc/sfu 房间、gameloop 房间、presence
// 这些**进程内单实例**的有状态服务能水平扩多副本——服务本身不用改成分布式,分片层坐在前面。
//
// 两块能力:
//   - Sharder:成员集合上的一致性哈希归属(基于 pkg/loadbalance.ConsistentHash),
//     Owner(key) 给出归属实例、IsLocal(key) 判断是否本机。成员集合可随服务发现动态更新
//     (SetMembers),归属在成员增减时最小化迁移。
//   - Router:http.Handler,按 key 把非本地请求反代给归属实例(WebSocket 也支持,
//     httputil.ReverseProxy 处理 Upgrade)。本地 key 交给本地 handler。
//
// 边界(机制而非策略):成员从哪来(服务发现)、key 怎么从请求里取、权重怎么定,都是
// policy。典型接法:
//
//	self := shard.StaticMember{NodeID: hostname, NodeAddr: "http://" + podIP + ":8090"}
//	sh := shard.New(self.ID(), self)
//	// 用 pkg/service/discover 监听实例变化,变了就 sh.SetMembers(all...)
//	hub := media.NewHub(func(k string) *hlsmux.Bridge { ... })
//	mux.Handle("/live/", http.StripPrefix("/live", shard.NewRouter(sh, shard.PathHeadKey, hub)))
//	// 播 /live/roomA/index.m3u8 → 若 roomA 不归本机,自动反代给归属实例
//
// 一致性说明:成员集合在各实例间靠服务发现最终一致,churn 期间可能短暂不一致(两台都认为
// 自己不是 owner)。Router 用一个"已代理"标记头防止无限转发:带标记又仍非本地时就地服务,
// 宁可短暂错分也不打转。要强一致的归属请在其上叠加租约(pkg/dlock)。
package shard

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/rushteam/beauty/pkg/loadbalance"
)

// Member 是分片环上的一个实例。ID 要稳定(用作哈希与去重);Addr 是反代非本地请求的
// 基地址(如 "http://10.0.0.3:8090" 或 "10.0.0.3:8090",无 scheme 时按 http 处理)。
type Member interface {
	ID() string
	Weight() int
	Addr() string
}

// StaticMember 是 Member 的简单实现。Weight<=0 视为 1。
type StaticMember struct {
	NodeID     string
	NodeWeight int
	NodeAddr   string
}

func (m StaticMember) ID() string { return m.NodeID }
func (m StaticMember) Weight() int {
	if m.NodeWeight <= 0 {
		return 1
	}
	return m.NodeWeight
}
func (m StaticMember) Addr() string { return m.NodeAddr }

// Sharder 维护成员集合并做一致性哈希归属。并发安全。零值不可用,用 New 构造。
type Sharder struct {
	self          string
	virtualFactor uint32

	mu      sync.RWMutex
	ring    *loadbalance.ConsistentHash[Member]
	members map[string]Member
}

// Option 配置 Sharder。
type Option func(*Sharder)

// WithVirtualFactor 设置每个实例的虚拟节点数(越大分布越均匀,默认 100)。
func WithVirtualFactor(n uint32) Option {
	return func(s *Sharder) {
		if n > 0 {
			s.virtualFactor = n
		}
	}
}

// New 创建 Sharder。self 是本实例 ID(须等于本实例在 members 里的 ID);members 是初始成员集。
func New(self string, members []Member, opts ...Option) *Sharder {
	s := &Sharder{self: self, virtualFactor: 100, members: map[string]Member{}}
	for _, o := range opts {
		o(s)
	}
	s.SetMembers(members)
	return s
}

// SetMembers 用新的成员集合重建哈希环(服务发现变更时调用)。并发安全。
func (s *Sharder) SetMembers(members []Member) {
	m := make(map[string]Member, len(members))
	list := make([]Member, 0, len(members))
	for _, mem := range members {
		if mem == nil || mem.ID() == "" {
			continue
		}
		if _, dup := m[mem.ID()]; dup {
			continue
		}
		m[mem.ID()] = mem
		list = append(list, mem)
	}
	ring := loadbalance.NewConsistentHash(list, loadbalance.WithVirtualFactor[Member](s.virtualFactor))
	s.mu.Lock()
	s.members = m
	s.ring = ring
	s.mu.Unlock()
}

// Owner 返回 key 的归属实例。成员集为空时返回 (nil, false)。
func (s *Sharder) Owner(key string) (Member, bool) {
	s.mu.RLock()
	ring := s.ring
	s.mu.RUnlock()
	if ring == nil {
		return nil, false
	}
	return ring.Get(key)
}

// IsLocal 判断 key 是否归属本实例。成员集为空(单机部署 / 尚未发现其他实例)时视为本地。
func (s *Sharder) IsLocal(key string) bool {
	owner, ok := s.Owner(key)
	return !ok || owner.ID() == s.self
}

// Self 返回本实例 ID。
func (s *Sharder) Self() string { return s.self }

// Members 返回当前成员集合(副本)。
func (s *Sharder) Members() []Member {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Member, 0, len(s.members))
	for _, m := range s.members {
		out = append(out, m)
	}
	return out
}

// proxyHeader 标记一个请求已被某实例反代过,用于防止 churn 期间的无限转发。
const proxyHeader = "X-Beauty-Shard-Proxied"

// PathHeadKey 从请求路径取第一段作为分片 key(与 media.Hub 的 /{key}/… 路由对齐)。
func PathHeadKey(r *http.Request) string {
	p := strings.TrimPrefix(r.URL.Path, "/")
	key, _, _ := strings.Cut(p, "/")
	return key
}

// Router 是分片反代 http.Handler:按 keyFn 取出分片 key,归属本机的交给 local,
// 归属他机的反向代理过去。并发安全。
type Router struct {
	sharder *Sharder
	keyFn   func(*http.Request) string
	local   http.Handler

	mu      sync.Mutex
	proxies map[string]*httputil.ReverseProxy // 按 Addr 缓存
}

// NewRouter 创建分片路由。keyFn 从请求提取分片 key(如 PathHeadKey);local 服务归属本机的 key。
func NewRouter(s *Sharder, keyFn func(*http.Request) string, local http.Handler) *Router {
	return &Router{sharder: s, keyFn: keyFn, local: local, proxies: map[string]*httputil.ReverseProxy{}}
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := rt.keyFn(r)
	if key == "" {
		rt.local.ServeHTTP(w, r) // 无 key(如根路径)→ 本地
		return
	}
	owner, ok := rt.sharder.Owner(key)
	if !ok || owner.ID() == rt.sharder.self {
		rt.local.ServeHTTP(w, r)
		return
	}
	// 防环:带过代理标记又仍非本地(环不一致)→ 就地服务,不再转发。
	if r.Header.Get(proxyHeader) != "" {
		rt.local.ServeHTTP(w, r)
		return
	}
	proxy, err := rt.proxyFor(owner.Addr())
	if err != nil {
		http.Error(w, "shard: bad owner addr: "+err.Error(), http.StatusBadGateway)
		return
	}
	r.Header.Set(proxyHeader, rt.sharder.self)
	proxy.ServeHTTP(w, r)
}

func (rt *Router) proxyFor(addr string) (*httputil.ReverseProxy, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if p, ok := rt.proxies[addr]; ok {
		return p, nil
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	base, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}
	p := httputil.NewSingleHostReverseProxy(base)
	rt.proxies[addr] = p
	return p, nil
}
