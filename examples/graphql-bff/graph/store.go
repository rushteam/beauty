package graph

import (
	"fmt"
	"sync"
	"time"
)

// Store 是内存假后端,模拟 user-svc / order-svc。
type Store struct {
	mu     sync.RWMutex
	users  map[string]*User
	orders map[string]*Order
	seq    int
}

func NewStore() *Store {
	now := time.Now().Format(time.RFC3339)
	return &Store{
		users: map[string]*User{
			"1": {ID: "1", Name: "Alice", Email: "alice@example.com"},
			"2": {ID: "2", Name: "Bob", Email: "bob@example.com"},
			"3": {ID: "3", Name: "Carol", Email: "carol@example.com"},
		},
		orders: map[string]*Order{
			"order-1": {ID: "order-1", UserID: "1", Total: 99.9, Status: "pending", Items: []string{"item-a"}, UpdatedAt: now},
			"order-2": {ID: "order-2", UserID: "1", Total: 49.5, Status: "shipped", Items: []string{"item-b", "item-c"}, UpdatedAt: now},
			"order-3": {ID: "order-3", UserID: "2", Total: 150.0, Status: "delivered", Items: []string{"item-d"}, UpdatedAt: now},
		},
		seq: 3,
	}
}

func (s *Store) GetUser(id string) *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u := s.users[id]
	if u == nil {
		return nil
	}
	cp := *u
	return &cp
}

func (s *Store) GetUsersByIDs(ids []string) map[string]*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*User, len(ids))
	for _, id := range ids {
		if u, ok := s.users[id]; ok {
			cp := *u
			out[id] = &cp
		}
	}
	return out
}

func (s *Store) ListUsers(limit int) []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0, limit)
	for _, u := range s.users {
		if len(out) >= limit {
			break
		}
		cp := *u
		out = append(out, &cp)
	}
	return out
}

func (s *Store) GetOrder(id string) *Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o := s.orders[id]
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

func (s *Store) OrdersByUserID(userID string) []*Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Order
	for _, o := range s.orders {
		if o.UserID == userID {
			cp := *o
			out = append(out, &cp)
		}
	}
	return out
}

func (s *Store) CreateOrder(input CreateOrderInput) *Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	o := &Order{
		ID:        fmt.Sprintf("order-%d", s.seq),
		UserID:    input.UserID,
		Total:     input.Total,
		Status:    "pending",
		Items:     append([]string(nil), input.Items...),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	s.orders[o.ID] = o
	cp := *o
	return &cp
}

// AdvanceOrderStatus 模拟订单状态推进(供 subscription demo)。
func (s *Store) AdvanceOrderStatus(id string) *Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.orders[id]
	if o == nil {
		return nil
	}
	switch o.Status {
	case "pending":
		o.Status = "paid"
	case "paid":
		o.Status = "shipped"
	case "shipped":
		o.Status = "delivered"
	}
	o.UpdatedAt = time.Now().Format(time.RFC3339)
	cp := *o
	return &cp
}
