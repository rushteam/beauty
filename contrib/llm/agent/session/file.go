package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileStore 把每个会话存成目录下的一个 JSON 文件(<id>.json)。零外部依赖的持久化实现,
// 适合单机/小流量;生产高并发可换 sqlite/redis 实现同一 Store 接口。
// id 经清洗后仅允许 [A-Za-z0-9._-],防止路径穿越。
type FileStore struct {
	dir string
	mu  sync.Mutex // 同进程内串行化读写;跨进程无锁(文件替换近似原子)
}

// NewFileStore 使用 dir 作为会话目录(不存在则创建)。
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("session: empty dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session: mkdir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(id string) (string, error) {
	safe, err := sanitizeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, safe+".json"), nil
}

func sanitizeID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("session: empty id")
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			return "", fmt.Errorf("session: invalid id %q", id)
		}
	}
	if strings.Contains(id, "..") {
		return "", fmt.Errorf("session: invalid id %q", id)
	}
	return id, nil
}

// Load 读取会话;不存在返回 (nil, nil)。
func (s *FileStore) Load(_ context.Context, id string) (*Session, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("session: decode %s: %w", id, err)
	}
	sess.ID = id
	return cloneSession(&sess), nil
}

// Save 原子写入(先写临时文件再 rename)。
func (s *FileStore) Save(_ context.Context, sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session: nil session")
	}
	path, err := s.path(sess.ID)
	if err != nil {
		return err
	}
	cp := cloneSession(sess)
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Delete 删除会话文件(不存在不报错)。
func (s *FileStore) Delete(_ context.Context, id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
