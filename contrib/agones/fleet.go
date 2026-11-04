package agones

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrFleetExists 同名 Fleet 已登记。名字比较忽略大小写。
	ErrFleetExists = errors.New("agones: fleet exists")
	// ErrFleetNotFound 名册里没有这个 Fleet。
	ErrFleetNotFound = errors.New("agones: fleet not found")
	// ErrFleetName 名字为空。
	ErrFleetName = errors.New("agones: empty fleet name")
	// ErrFleetAllocator Allocator 为空。
	ErrFleetAllocator = errors.New("agones: nil allocator")
)

// Fleet 是一块可分配的 GameServer 池。
// Namespace 在登记时确定;Allocate 的请求没写 Namespace 时用它。
type Fleet struct {
	Name      string
	Namespace string
	Alloc     Allocator
}

// Roster 按名字登记多块 Fleet。一个匹配服可以同时向多个命名空间领房间。
type Roster struct {
	mu    sync.RWMutex
	items map[string]Fleet
}

// NewRoster 创建空名册。
func NewRoster() *Roster {
	return &Roster{items: make(map[string]Fleet)}
}

// Add 登记一块 Fleet。Namespace 为空时记为 default。
func (r *Roster) Add(f Fleet) error {
	name := strings.TrimSpace(f.Name)
	if name == "" {
		return ErrFleetName
	}
	if f.Alloc == nil {
		return ErrFleetAllocator
	}
	ns := strings.TrimSpace(f.Namespace)
	if ns == "" {
		ns = "default"
	}
	key := strings.ToLower(name)
	stored := Fleet{Name: name, Namespace: ns, Alloc: f.Alloc}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[key]; ok {
		return ErrFleetExists
	}
	r.items[key] = stored
	return nil
}

// Get 按名字取 Fleet。找不到时 ok 为 false。
func (r *Roster) Get(name string) (Fleet, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.items[strings.ToLower(strings.TrimSpace(name))]
	return f, ok
}

// Names 返回已登记的 Fleet 名,按名字排序。
func (r *Roster) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for _, f := range r.items {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// Allocate 从指名的 Fleet 领一台 GameServer。
// req.Namespace 为空时改用登记时的 Namespace。
func (r *Roster) Allocate(ctx context.Context, name string, req AllocationRequest) (AllocationResult, error) {
	f, ok := r.Get(name)
	if !ok {
		return AllocationResult{}, ErrFleetNotFound
	}
	if strings.TrimSpace(req.Namespace) == "" {
		req.Namespace = f.Namespace
	}
	return f.Alloc.Allocate(ctx, req)
}
