package matchmaker

import (
	"context"
	"fmt"
	"sync"
)

// RatingStore 评分存储接口,业务实现持久化(内存/Redis/DB)。
type RatingStore interface {
	Get(userID string) (skill float64, err error)
	Set(userID string, skill float64) error
}

// Rater 根据参与者与对局得分计算新 skill。
// result 的 value 是该玩家得分(1=胜,0.5=平,0=负);
// 返回 userID → 新 skill,由 ApplyResult 写回 store。
type Rater func(participants []string, result map[string]float64) map[string]float64

// RatingHandler 包装匹配成功回调。inner 负责开房间等业务;
// 匹配时尚无胜负,只把 store 中的最新 skill 填回 ticket 便于下游展示。
// 对局结束后调用 ApplyResult(store, rater, ...) 把胜负写回评分。
func RatingHandler(inner Handler, store RatingStore, rater Rater) Handler {
	if inner == nil {
		inner = func(context.Context, Match) error { return nil }
	}
	return func(ctx context.Context, m Match) error {
		if err := inner(ctx, m); err != nil {
			return err
		}
		if store == nil {
			return nil
		}
		for _, t := range m.Tickets {
			if t == nil || t.Presence.UserID == "" {
				continue
			}
			skill, err := store.Get(t.Presence.UserID)
			if err != nil {
				continue
			}
			if t.Properties.Numeric == nil {
				t.Properties.Numeric = make(map[string]float64)
			}
			t.Properties.Numeric[AttrSkill] = skill
		}
		return nil
	}
}

// ApplyResult 根据对局结果更新所有参与者的评分。
// participants 为参与者 ID,result 为 userID → 得分(1/0.5/0)。
func ApplyResult(store RatingStore, rater Rater, participants []string, result map[string]float64) error {
	if store == nil {
		return fmt.Errorf("matchmaker: nil rating store")
	}
	if rater == nil {
		return fmt.Errorf("matchmaker: nil rater")
	}
	updated := rater(participants, result)
	for uid, skill := range updated {
		if err := store.Set(uid, skill); err != nil {
			return err
		}
	}
	return nil
}

// MemoryStore 内存 RatingStore,便于测试与单机演示。并发安全。
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]float64
}

// NewMemoryStore 创建空的内存评分存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: make(map[string]float64)}
}

// Get 读取 userID 的 skill;不存在返回 0, nil error。
func (s *MemoryStore) Get(userID string) (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[userID], nil
}

// Set 写入 userID 的 skill。
func (s *MemoryStore) Set(userID string, skill float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[userID] = skill
	return nil
}
