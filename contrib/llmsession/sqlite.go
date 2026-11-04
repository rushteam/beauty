// Package llmsession 为 llm/agent/session 提供生产向 Store:SQLite 与 Redis。
// 独立模块——不拖重 llm 的零依赖核心;用什么才引什么。
package llmsession

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
	_ "modernc.org/sqlite"
)

// SQLiteStore 用单库文件持久化会话(每行一个 session)。并发安全(靠 SQLite 锁)。
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLite 打开/创建 path 处的 sqlite 库并建表。path 可用 ":memory:" 做测试。
func NewSQLite(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("llmsession: empty sqlite path")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite 写串行更稳
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  summary TEXT NOT NULL DEFAULT '',
  messages BLOB NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS session_events (
  id TEXT PRIMARY KEY,
  events BLOB NOT NULL
)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("llmsession: migrate: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN extra TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		_ = db.Close()
		return nil, fmt.Errorf("llmsession: migrate extra: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Close 关闭底层 DB。
func (s *SQLiteStore) Close() error { return s.db.Close() }

// Load 实现 session.Store。
func (s *SQLiteStore) Load(ctx context.Context, id string) (*session.Session, error) {
	var summary, extra string
	var raw []byte
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT summary, messages, updated_at, extra FROM sessions WHERE id=?`, id).
		Scan(&summary, &raw, &updated, &extra)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess := &session.Session{ID: id, Summary: summary, UpdatedAt: time.Unix(updated, 0).UTC()}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &sess.Messages); err != nil {
			return nil, fmt.Errorf("llmsession: decode messages: %w", err)
		}
	}
	if len(extra) > 0 {
		var side sessionSide
		if err := json.Unmarshal([]byte(extra), &side); err != nil {
			return nil, fmt.Errorf("llmsession: decode extra: %w", err)
		}
		sess.PendingRunID = side.PendingRunID
		sess.Usage = side.Usage
		sess.Runs = side.Runs
		sess.Metadata = side.Metadata
	}
	return sess, nil
}

// Save 实现 session.Store。
func (s *SQLiteStore) Save(ctx context.Context, sess *session.Session) error {
	if sess == nil || sess.ID == "" {
		return fmt.Errorf("llmsession: invalid session")
	}
	raw, err := json.Marshal(sess.Messages)
	if err != nil {
		return err
	}
	updated := sess.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	side, err := json.Marshal(sessionSide{
		PendingRunID: sess.PendingRunID,
		Usage:        sess.Usage,
		Runs:         sess.Runs,
		Metadata:     sess.Metadata,
	})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO sessions(id, summary, messages, updated_at, extra) VALUES(?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  summary=excluded.summary,
  messages=excluded.messages,
  updated_at=excluded.updated_at,
  extra=excluded.extra
`, sess.ID, sess.Summary, raw, updated.Unix(), side)
	return err
}

type sessionSide struct {
	PendingRunID string                     `json:"pending_run_id,omitempty"`
	Usage        llm.Usage                  `json:"usage,omitempty"`
	Runs         []session.Run              `json:"runs,omitempty"`
	Metadata     map[string]json.RawMessage `json:"metadata,omitempty"`
}

// AppendEvents 实现 session.EventStore。
func (s *SQLiteStore) AppendEvents(ctx context.Context, sessionID string, events ...session.SessionEvent) error {
	if len(events) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for i := range events {
		if events[i].Timestamp.IsZero() {
			events[i].Timestamp = now
		}
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return err
	}
	// 使用 JSON 追加:读取现有 → 合并 → 写回。SQLite 单连接串行化保证安全。
	var existing []byte
	_ = s.db.QueryRowContext(ctx, `SELECT events FROM session_events WHERE id=?`, sessionID).Scan(&existing)
	if len(existing) > 0 {
		var prev []session.SessionEvent
		if err := json.Unmarshal(existing, &prev); err == nil {
			prev = append(prev, events...)
			raw, err = json.Marshal(prev)
			if err != nil {
				return err
			}
		}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO session_events(id, events) VALUES(?,?)
ON CONFLICT(id) DO UPDATE SET events=excluded.events
`, sessionID, raw)
	return err
}

// LoadEvents 实现 session.EventStore。
func (s *SQLiteStore) LoadEvents(ctx context.Context, sessionID string) ([]session.SessionEvent, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT events FROM session_events WHERE id=?`, sessionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []session.SessionEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("llmsession: decode events: %w", err)
	}
	return events, nil
}

var (
	_ session.Store      = (*SQLiteStore)(nil)
	_ session.EventStore = (*SQLiteStore)(nil)
)
