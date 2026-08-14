// Package llmcheckpoint 为 llm/agent CheckpointStore 提供 SQLite / Redis 持久化。
// 独立模块——不拖重 llm 零依赖核心;用什么才引什么。
package llmcheckpoint

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
	_ "modernc.org/sqlite"
)

// SQLiteStore 持久化 RunSnapshot 与 checkpoint 事件日志。
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLite 打开/创建 path 处的 sqlite 库并建表。path 可用 ":memory:" 做测试。
func NewSQLite(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("llmcheckpoint: empty sqlite path")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS run_snapshots (
  id TEXT PRIMARY KEY,
  snapshot BLOB NOT NULL,
  updated_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS run_events (
  id TEXT PRIMARY KEY,
  events BLOB NOT NULL
)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("llmcheckpoint: migrate: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Close 关闭底层 DB。
func (s *SQLiteStore) Close() error { return s.db.Close() }

// Save 实现 agent.RunStore。
func (s *SQLiteStore) Save(ctx context.Context, id string, snap *agent.RunSnapshot) error {
	if id == "" || snap == nil {
		return fmt.Errorf("llmcheckpoint: Save requires id and snapshot")
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO run_snapshots(id, snapshot, updated_at) VALUES(?,?,strftime('%s','now'))
ON CONFLICT(id) DO UPDATE SET snapshot=excluded.snapshot, updated_at=strftime('%s','now')
`, id, raw)
	return err
}

// Load 实现 agent.RunStore。
func (s *SQLiteStore) Load(ctx context.Context, id string) (*agent.RunSnapshot, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT snapshot FROM run_snapshots WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap agent.RunSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("llmcheckpoint: decode snapshot: %w", err)
	}
	return &snap, nil
}

// Delete 删除暂停快照;事件日志保留供 UI 回放。
func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM run_snapshots WHERE id=?`, id)
	return err
}

// AppendEvents 实现 checkpoint.EventLog。
func (s *SQLiteStore) AppendEvents(ctx context.Context, runID string, events ...checkpoint.Event) error {
	if len(events) == 0 {
		return nil
	}
	var existing []byte
	_ = s.db.QueryRowContext(ctx, `SELECT events FROM run_events WHERE id=?`, runID).Scan(&existing)
	var prev []checkpoint.Event
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &prev); err != nil {
			return fmt.Errorf("llmcheckpoint: decode events: %w", err)
		}
	}
	prev = append(prev, events...)
	raw, err := json.Marshal(prev)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO run_events(id, events) VALUES(?,?)
ON CONFLICT(id) DO UPDATE SET events=excluded.events
`, runID, raw)
	return err
}

// LoadEvents 实现 checkpoint.EventLog。
func (s *SQLiteStore) LoadEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT events FROM run_events WHERE id=?`, runID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []checkpoint.Event
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, fmt.Errorf("llmcheckpoint: decode events: %w", err)
	}
	return events, nil
}

// EventCount 实现 checkpoint.EventLog。
func (s *SQLiteStore) EventCount(ctx context.Context, runID string) (int, error) {
	events, err := s.LoadEvents(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(events), nil
}

var _ agent.CheckpointStore = (*SQLiteStore)(nil)
