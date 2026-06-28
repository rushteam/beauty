// Package resume 提供断线重连的在场状态还原:客户端用 refresh token 重连,
// 服务端换出 userID,查 presence 看用户还在哪些流,返回流列表让客户端自动重连。
//
// session resume:断线后 presence 不立即
// 清除(给一个宽限期),重连时把该会话仍持有的流回给客户端,客户端按列表重新 join,
// 实现"掉线不掉状态"的体验。
//
// 它把 pkg/token(验证 refresh)与 pkg/presence(查在场流)织成完整登录态:
//   token.VerifyRefresh → userID/tokenID → presence.ListBySession → []Stream
//
// 零值不可用,用 New 构造。并发安全(只读组合两个并发安全组件)。
package resume

import (
	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/token"
)

// Stream 流的精简视图(只含客户端重连需要的字段)。
type Stream struct {
	Mode    uint8
	Subject string
	Label   string
}

// PresenceInfo 一个会话的在场快照:在哪些流 + 对应 userID。
type PresenceInfo struct {
	UserID   string
	TokenID  string
	Streams  []Stream
}

// Resolver 把 refresh token 解析为在场快照。
type Resolver struct {
	tm       *token.Manager
	tracker  *presence.Tracker
}

// Option 配置 Resolver。
type Option func(*config)

type config struct {
	tm      *token.Manager
	tracker *presence.Tracker
}

// WithTokenManager 设置 token 管理器(必填,用于验证 refresh token)。
func WithTokenManager(m *token.Manager) Option { return func(c *config) { c.tm = m } }

// WithTracker 设置在场追踪器(必填,用于查询用户的流)。
func WithTracker(t *presence.Tracker) Option { return func(c *config) { c.tracker = t } }

// New 创建 Resolver。tm 与 tracker 均必填,缺一则 Resolve 返回错误。
func New(opts ...Option) *Resolver {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	return &Resolver{tm: cfg.tm, tracker: cfg.tracker}
}

// ErrInvalidToken refresh token 无效。
var ErrInvalidToken = token.ErrInvalidToken

// ErrExpired refresh token 已过期。
var ErrExpired = token.ErrExpired

// ErrRevoked refresh token 已被注销。
var ErrRevoked = token.ErrRevoked

// ErrKicked 用户已被全局踢出。
var ErrKicked = token.ErrKicked

// ErrNotConfigured token 管理器或 tracker 未配置。
var ErrNotConfigured = errNotConfigured{}

type errNotConfigured struct{}

func (errNotConfigured) Error() string { return "resume: not configured" }

// Resolve 用 refresh token 还原在场快照。
// 流程:VerifyRefresh 换 claims → 用 sessionID(=tokenID)查 presence.ListBySession
// → 投影成 Stream 列表。
//
// sessionID 的约定:业务在 Issue 时把 tokenID 作为 presence.Track 的 sessionID
// (或建立 sessionID↔tokenID 映射),本包按此约定查询。
// 若业务用别的 sessionID 方案,可用 ResolveBySessionID 直接传 sessionID。
func (r *Resolver) Resolve(refreshToken string) (PresenceInfo, error) {
	if r.tm == nil || r.tracker == nil {
		return PresenceInfo{}, ErrNotConfigured
	}
	c, err := r.tm.VerifyRefresh(refreshToken)
	if err != nil {
		return PresenceInfo{}, err
	}
	return r.resolveByClaims(c), nil
}

// ResolveBySessionID 直接按 sessionID 还原在场快照(不走 token 验证)。
// 适用于业务自管 sessionID、只需查在场流的场景。
func (r *Resolver) ResolveBySessionID(sessionID string) (PresenceInfo, error) {
	if r.tracker == nil {
		return PresenceInfo{}, ErrNotConfigured
	}
	return PresenceInfo{
		TokenID: sessionID,
		Streams: r.streamsOf(sessionID),
	}, nil
}

// MarkOnline 在重连后把新会话重新登记到 presence(便于后续 ListBySession 命中)。
// 调用方应在重连握手成功后、对每个返回的 Stream 调 Track,或直接用本方法批量重登。
// userID/username 从 token claims 取(sessionID 走不到 token 时留空)。
func (r *Resolver) MarkOnline(sessionID, userID, username string, streams []Stream, hidden bool) {
	if r.tracker == nil {
		return
	}
	meta := presence.Meta{UserID: userID, Username: username, Hidden: hidden}
	for _, s := range streams {
		r.tracker.Track(sessionID, toPresenceStream(s), meta)
	}
}

func (r *Resolver) resolveByClaims(c *token.Claims) PresenceInfo {
	return PresenceInfo{
		UserID:  c.UserID,
		TokenID: c.TokenID,
		Streams: r.streamsOf(c.TokenID),
	}
}

func (r *Resolver) streamsOf(sessionID string) []Stream {
	members := r.tracker.ListBySession(sessionID)
	if len(members) == 0 {
		return nil
	}
	out := make([]Stream, 0, len(members))
	for _, p := range members {
		out = append(out, Stream{
			Mode:    p.ID.Stream.Mode,
			Subject: p.ID.Stream.Subject,
			Label:   p.ID.Stream.Label,
		})
	}
	return out
}

func toPresenceStream(s Stream) presence.Stream {
	return presence.Stream{Mode: s.Mode, Subject: s.Subject, Label: s.Label}
}
