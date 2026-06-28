package resume_test

import (
	"errors"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/resume"
	"github.com/rushteam/beauty/pkg/token"
)

func newMgr(t *testing.T) (*token.Manager, *presence.Tracker, *resume.Resolver) {
	t.Helper()
	tm := token.New(
		token.WithSessionKey([]byte("sess-secret-32-bytes-aaaaaaaaaa")),
		token.WithRefreshKey([]byte("refresh-secret-different-32b")),
		token.WithSessionTTL(time.Hour),
		token.WithRefreshTTL(24*time.Hour),
	)
	t.Cleanup(tm.Stop)
	tr := presence.New(nil, 16)
	r := resume.New(resume.WithTokenManager(tm), resume.WithTracker(tr))
	return tm, tr, r
}

func TestResolver_Resolve_NoPresence(t *testing.T) {
	tm, _, r := newMgr(t)
	_, refresh, _ := tm.Issue("u1", "alice", nil, "tid-1")
	info, err := r.Resolve(refresh)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.UserID != "u1" || info.TokenID != "tid-1" {
		t.Fatalf("claims lost: %+v", info)
	}
	if len(info.Streams) != 0 {
		t.Fatalf("want 0 streams, got %d", len(info.Streams))
	}
}

func TestResolver_Resolve_WithPresence(t *testing.T) {
	tm, tr, r := newMgr(t)
	_, refresh, _ := tm.Issue("u1", "alice", nil, "tid-1")
	// 模拟断线前:用 tokenID 作为 sessionID 登记到两个流。
	tr.Track("tid-1", presence.Stream{Mode: 1, Subject: "room1"}, presence.Meta{UserID: "u1"})
	tr.Track("tid-1", presence.Stream{Mode: 2, Subject: "party-a"}, presence.Meta{UserID: "u1"})

	info, err := r.Resolve(refresh)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.UserID != "u1" || info.TokenID != "tid-1" {
		t.Fatalf("claims lost: %+v", info)
	}
	if len(info.Streams) != 2 {
		t.Fatalf("want 2 streams, got %d", len(info.Streams))
	}
	// 应包含两个 subject。
	got := map[string]bool{}
	for _, s := range info.Streams {
		got[s.Subject] = true
	}
	if !got["room1"] || !got["party-a"] {
		t.Fatalf("missing streams: %+v", info.Streams)
	}
}

func TestResolver_Resolve_InvalidToken(t *testing.T) {
	_, _, r := newMgr(t)
	_, err := r.Resolve("not-a-jwt")
	if !errors.Is(err, resume.ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestResolver_Resolve_Expired(t *testing.T) {
	tm := token.New(
		token.WithSessionKey([]byte("k")),
		token.WithRefreshKey([]byte("r")),
		token.WithSessionTTL(time.Hour),
		token.WithRefreshTTL(-time.Second), // refresh 已过期
	)
	defer tm.Stop()
	tr := presence.New(nil, 16)
	r := resume.New(resume.WithTokenManager(tm), resume.WithTracker(tr))

	_, refresh, _ := tm.Issue("u1", "", nil, "tid")
	_, err := r.Resolve(refresh)
	if !errors.Is(err, resume.ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestResolver_Resolve_Revoked(t *testing.T) {
	tm, _, r := newMgr(t)
	_, refresh, _ := tm.Issue("u1", "", nil, "tid")
	tm.Revoke("tid")
	_, err := r.Resolve(refresh)
	if !errors.Is(err, resume.ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
}

func TestResolver_Resolve_Kicked(t *testing.T) {
	tm, _, r := newMgr(t)
	_, refresh, _ := tm.Issue("u1", "", nil, "tid")
	tm.RevokeAll("u1")
	// RevokeAll 后,iat <= kickedAt 的 token 失效。本 token 在 kick 前签发,应被踢。
	_, err := r.Resolve(refresh)
	if !errors.Is(err, resume.ErrKicked) {
		t.Fatalf("want ErrKicked, got %v", err)
	}
}

func TestResolver_ResolveBySessionID(t *testing.T) {
	_, tr, r := newMgr(t)
	tr.Track("sess-99", presence.Stream{Mode: 1, Subject: "room-x"}, presence.Meta{UserID: "u9"})
	info, err := r.ResolveBySessionID("sess-99")
	if err != nil {
		t.Fatalf("resolve by sid: %v", err)
	}
	if info.TokenID != "sess-99" {
		t.Fatalf("tokenID=%s want sess-99", info.TokenID)
	}
	if len(info.Streams) != 1 || info.Streams[0].Subject != "room-x" {
		t.Fatalf("streams: %+v", info.Streams)
	}
}

func TestResolver_MarkOnline_ReRegisters(t *testing.T) {
	_, tr, r := newMgr(t)
	streams := []resume.Stream{
		{Mode: 1, Subject: "r1"},
		{Mode: 2, Subject: "p1"},
	}
	// 重连后用新 sessionID 重新登记。
	r.MarkOnline("new-sess", "u1", "alice", streams, false)
	got := tr.ListBySession("new-sess")
	if len(got) != 2 {
		t.Fatalf("after MarkOnline want 2, got %d", len(got))
	}
}

func TestResolver_NotConfigured(t *testing.T) {
	r := resume.New() // 无 tm 无 tracker
	if _, err := r.Resolve("x"); !errors.Is(err, resume.ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	if _, err := r.ResolveBySessionID("x"); !errors.Is(err, resume.ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestResolver_Resolve_AfterResumeCycle(t *testing.T) {
	// 完整重连周期:断线 → Resolve 拿流 → MarkOnline 用新 sessionID 重登 → 新 sessionID 可查。
	tm, tr, r := newMgr(t)
	_, refresh, _ := tm.Issue("u1", "alice", nil, "tid-old")
	tr.Track("tid-old", presence.Stream{Mode: 1, Subject: "room1"}, presence.Meta{UserID: "u1"})

	// 断线重连:用 refresh 还原。
	info, err := r.Resolve(refresh)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// 用新 sessionID 重新登记(模拟客户端重连后用新连接的 session)。
	r.MarkOnline("tid-new", info.UserID, "alice", info.Streams, false)
	// 新 sessionID 应能查到。
	if got := tr.ListBySession("tid-new"); len(got) != 1 || got[0].ID.Stream.Subject != "room1" {
		t.Fatalf("re-login failed: %+v", got)
	}
}
