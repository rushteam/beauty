// Package billing 是支付渠道注册表:按名字校验购买、校验订阅,并把渠道回调解析成变更。
//
// 本包不认识任何商店的收据格式。验签、HTTP 和发货都由调用方完成;
// 渠道只通过函数交回 Order / Plan / Change。
//
// 零值不可用,用 New 构造。Registry 并发安全。
package billing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrExists 同名渠道已注册。名字比较忽略大小写。
	ErrExists = errors.New("billing: channel exists")
	// ErrNotFound 没有这个渠道或回调名。
	ErrNotFound = errors.New("billing: channel not found")
	// ErrInvalidName 渠道名或回调名不合法。
	ErrInvalidName = errors.New("billing: invalid channel name")
	// ErrEmpty 渠道没有提供任何能力。
	ErrEmpty = errors.New("billing: empty channel")
	// ErrNoVerify 该渠道不校验一次性购买。
	ErrNoVerify = errors.New("billing: channel does not verify purchases")
	// ErrNoPlan 该渠道不校验订阅。
	ErrNoPlan = errors.New("billing: channel does not verify plans")
	// ErrNoNotice 该渠道不解析回调。
	ErrNoNotice = errors.New("billing: channel does not parse notices")
)

// nameRE 限制渠道名,便于直接放进回调路径。
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Kind 是渠道回调表达的变更。零值表示调用方未分类。
type Kind int

const (
	KindUnknown Kind = iota
	KindRefunded
	KindRenewed
	KindExpired
	KindCanceled
)

// Claim 是一次待校验的购买或订阅。Payload 与 Raw 对注册表不透明。
type Claim struct {
	UserID  string
	Product string
	Payload map[string]string
	Raw     []byte
}

// Order 是一次性购买的校验结果。
type Order struct {
	Ref     string
	UserID  string
	Product string
}

// Plan 是订阅的校验结果。Active 为 false 表示渠道认为当前不可用。
type Plan struct {
	Ref     string
	UserID  string
	Product string
	Until   time.Time
	Active  bool
}

// Notice 是渠道打到回调地址上的原始请求。Header 与 Query 由调用方从 HTTP 填入。
type Notice struct {
	Body   []byte
	Header map[string][]string
	Query  map[string][]string
}

// Change 是从回调里解析出的一条变更。
type Change struct {
	Kind    Kind
	Ref     string
	UserID  string
	Product string
	Until   time.Time
}

// Channel 是一个支付渠道。三个函数按需填写,至少填一个。
// Hook 是回调路径上的名字;空则用渠道名。
type Channel struct {
	Hook        string
	Verify      func(ctx context.Context, claim Claim) (Order, error)
	VerifyPlan  func(ctx context.Context, claim Claim) (Plan, error)
	ParseNotice func(ctx context.Context, notice Notice) ([]Change, error)
}

type entry struct {
	name string
	hook string
	ch   Channel
}

// Registry 按名字保存渠道。
type Registry struct {
	mu     sync.RWMutex
	byName map[string]*entry
	byHook map[string]*entry
}

// New 创建空注册表。
func New() *Registry {
	return &Registry{
		byName: make(map[string]*entry),
		byHook: make(map[string]*entry),
	}
}

// Add 注册渠道。同名或同一 Hook 返回 ErrExists。
func (r *Registry) Add(name string, ch Channel) error {
	display, key, err := normalizeName(name)
	if err != nil {
		return err
	}
	if ch.Verify == nil && ch.VerifyPlan == nil && ch.ParseNotice == nil {
		return ErrEmpty
	}
	hookDisplay := display
	hookKey := key
	if strings.TrimSpace(ch.Hook) != "" {
		hookDisplay, hookKey, err = normalizeName(ch.Hook)
		if err != nil {
			return fmt.Errorf("billing: hook: %w", err)
		}
	}
	ch.Hook = hookDisplay

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byName[key]; ok {
		return ErrExists
	}
	if _, ok := r.byHook[hookKey]; ok {
		return ErrExists
	}
	e := &entry{name: display, hook: hookDisplay, ch: ch}
	r.byName[key] = e
	r.byHook[hookKey] = e
	return nil
}

// Names 返回已注册渠道名,按名字排序。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byName))
	for _, e := range r.byName {
		out = append(out, e.name)
	}
	sort.Strings(out)
	return out
}

// Verify 校验一次性购买。
func (r *Registry) Verify(ctx context.Context, name string, claim Claim) (Order, error) {
	e, err := r.lookup(name)
	if err != nil {
		return Order{}, err
	}
	if e.ch.Verify == nil {
		return Order{}, ErrNoVerify
	}
	return e.ch.Verify(ctx, claim)
}

// VerifyPlan 校验订阅。
func (r *Registry) VerifyPlan(ctx context.Context, name string, claim Claim) (Plan, error) {
	e, err := r.lookup(name)
	if err != nil {
		return Plan{}, err
	}
	if e.ch.VerifyPlan == nil {
		return Plan{}, ErrNoPlan
	}
	return e.ch.VerifyPlan(ctx, claim)
}

// ParseNotice 按渠道名解析回调。
func (r *Registry) ParseNotice(ctx context.Context, name string, notice Notice) ([]Change, error) {
	e, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	return parse(ctx, e, notice)
}

// ParseHook 按回调名解析。HTTP 层只看到路径片段时用这个。
func (r *Registry) ParseHook(ctx context.Context, hook string, notice Notice) ([]Change, error) {
	_, key, err := normalizeName(hook)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	e := r.byHook[key]
	r.mu.RUnlock()
	if e == nil {
		return nil, ErrNotFound
	}
	return parse(ctx, e, notice)
}

func (r *Registry) lookup(name string) (*entry, error) {
	_, key, err := normalizeName(name)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	e := r.byName[key]
	r.mu.RUnlock()
	if e == nil {
		return nil, ErrNotFound
	}
	return e, nil
}

func parse(ctx context.Context, e *entry, notice Notice) ([]Change, error) {
	if e.ch.ParseNotice == nil {
		return nil, ErrNoNotice
	}
	return e.ch.ParseNotice(ctx, notice)
}

func normalizeName(name string) (display, key string, err error) {
	display = strings.TrimSpace(name)
	if !nameRE.MatchString(display) {
		return "", "", ErrInvalidName
	}
	return display, strings.ToLower(display), nil
}
