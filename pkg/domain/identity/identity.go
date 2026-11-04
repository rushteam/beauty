// Package identity 把外部登录渠道绑定到本地账号。
//
// 渠道只负责把凭据解析成外部身份(Resolver)。开户、发 token、要不要导入邮箱
// 都由调用方决定。本包只保存「渠道 + 外部 id ↔ 本地账号」的内存索引。
//
// 零值不可用,用 New 构造。Directory 并发安全。
package identity

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrExists 同名渠道已注册。名字比较忽略大小写。
	ErrExists = errors.New("identity: channel exists")
	// ErrNotFound 没有这个渠道,或该账号在该渠道上没有绑定。
	ErrNotFound = errors.New("identity: not found")
	// ErrUnbound 凭据有效,但外部身份还没绑到本地账号。用 errors.As 取 UnboundError。
	ErrUnbound = errors.New("identity: unbound")
	// ErrTaken 这个外部身份已经绑到别的账号。
	ErrTaken = errors.New("identity: external id already bound")
	// ErrBound 该账号在此渠道上已有绑定。先 Release 再改绑。
	ErrBound = errors.New("identity: account already bound on channel")
	// ErrInvalidName 渠道名不合法。
	ErrInvalidName = errors.New("identity: invalid channel name")
	// ErrInvalidPrincipal 外部 id 为空。
	ErrInvalidPrincipal = errors.New("identity: empty external id")
	// ErrInvalidAccount 本地账号 id 为空。
	ErrInvalidAccount = errors.New("identity: empty account id")
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Credential 是交给渠道的凭据。字段含义由渠道自己解释。
type Credential struct {
	Token   string
	Payload map[string]string
}

// Principal 是渠道认下的外部身份。
type Principal struct {
	ExternalID string
	Name       string
	Vars       map[string]string
}

// Binding 是一条已保存的绑定。
type Binding struct {
	AccountID  string
	Channel    string
	ExternalID string
	Name       string
	Vars       map[string]string
}

// Resolver 把凭据解析成外部身份。不创建、不查找本地账号。
type Resolver interface {
	Resolve(ctx context.Context, cred Credential) (Principal, error)
}

// ResolverFunc 让普通函数实现 Resolver。
type ResolverFunc func(ctx context.Context, cred Credential) (Principal, error)

// Resolve 实现 Resolver。
func (f ResolverFunc) Resolve(ctx context.Context, cred Credential) (Principal, error) {
	return f(ctx, cred)
}

// UnboundError 表示 SignIn 解析成功但尚未绑定。Principal 可直接拿去开户再 Bind。
type UnboundError struct {
	Principal Principal
}

func (e *UnboundError) Error() string { return ErrUnbound.Error() }

func (e *UnboundError) Unwrap() error { return ErrUnbound }

// Directory 是渠道注册表加绑定索引。
type Directory struct {
	mu         sync.Mutex
	resolvers  map[string]namedResolver
	byExternal map[string]Binding
	byAccount  map[string]map[string]Binding
}

type namedResolver struct {
	name     string
	resolver Resolver
}

// New 创建空目录。
func New() *Directory {
	return &Directory{
		resolvers:  make(map[string]namedResolver),
		byExternal: make(map[string]Binding),
		byAccount:  make(map[string]map[string]Binding),
	}
}

// Add 注册渠道。
func (d *Directory) Add(channel string, resolver Resolver) error {
	display, key, err := normalizeName(channel)
	if err != nil {
		return err
	}
	if resolver == nil {
		return errors.New("identity: nil resolver")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.resolvers[key]; ok {
		return ErrExists
	}
	d.resolvers[key] = namedResolver{name: display, resolver: resolver}
	return nil
}

// Resolve 只解析凭据,不查绑定。
func (d *Directory) Resolve(ctx context.Context, channel string, cred Credential) (Principal, error) {
	resolver, _, err := d.resolver(channel)
	if err != nil {
		return Principal{}, err
	}
	p, err := resolver.Resolve(ctx, cred)
	if err != nil {
		return Principal{}, err
	}
	if strings.TrimSpace(p.ExternalID) == "" {
		return Principal{}, ErrInvalidPrincipal
	}
	p.ExternalID = strings.TrimSpace(p.ExternalID)
	p.Vars = cloneVars(p.Vars)
	return p, nil
}

// SignIn 解析凭据并查找绑定。尚未绑定时返回 *UnboundError。
func (d *Directory) SignIn(ctx context.Context, channel string, cred Credential) (Binding, error) {
	p, err := d.Resolve(ctx, channel, cred)
	if err != nil {
		return Binding{}, err
	}
	if b, ok := d.Find(channel, p.ExternalID); ok {
		return b, nil
	}
	return Binding{}, &UnboundError{Principal: p}
}

// Bind 把外部身份绑到本地账号。同一对 (账号, 渠道, 外部 id) 再次绑定会更新 Name 与 Vars。
func (d *Directory) Bind(accountID, channel string, p Principal) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ErrInvalidAccount
	}
	display, key, err := d.channelKey(channel)
	if err != nil {
		return err
	}
	p.ExternalID = strings.TrimSpace(p.ExternalID)
	if p.ExternalID == "" {
		return ErrInvalidPrincipal
	}
	extKey := externalKey(key, p.ExternalID)

	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.byExternal[extKey]; ok && prev.AccountID != accountID {
		return ErrTaken
	}
	if existing, ok := d.byAccount[accountID][key]; ok && existing.ExternalID != p.ExternalID {
		return ErrBound
	}
	b := Binding{
		AccountID:  accountID,
		Channel:    display,
		ExternalID: p.ExternalID,
		Name:       p.Name,
		Vars:       cloneVars(p.Vars),
	}
	d.byExternal[extKey] = b
	acc := d.byAccount[accountID]
	if acc == nil {
		acc = make(map[string]Binding)
		d.byAccount[accountID] = acc
	}
	acc[key] = b
	return nil
}

// Release 解除某账号在某渠道上的绑定。
func (d *Directory) Release(accountID, channel string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ErrInvalidAccount
	}
	_, key, err := d.channelKey(channel)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	acc := d.byAccount[accountID]
	b, ok := acc[key]
	if !ok {
		return ErrNotFound
	}
	delete(acc, key)
	if len(acc) == 0 {
		delete(d.byAccount, accountID)
	}
	delete(d.byExternal, externalKey(key, b.ExternalID))
	return nil
}

// Find 按渠道和外部 id 查绑定。
func (d *Directory) Find(channel, externalID string) (Binding, bool) {
	_, key, err := normalizeName(channel)
	if err != nil {
		return Binding{}, false
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return Binding{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.byExternal[externalKey(key, externalID)]
	if !ok {
		return Binding{}, false
	}
	b.Vars = cloneVars(b.Vars)
	return b, true
}

// List 返回某账号的全部绑定,按渠道名排序。
func (d *Directory) List(accountID string) []Binding {
	accountID = strings.TrimSpace(accountID)
	d.mu.Lock()
	defer d.mu.Unlock()
	acc := d.byAccount[accountID]
	out := make([]Binding, 0, len(acc))
	for _, b := range acc {
		b.Vars = cloneVars(b.Vars)
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
}

func (d *Directory) resolver(channel string) (Resolver, string, error) {
	_, key, err := normalizeName(channel)
	if err != nil {
		return nil, "", err
	}
	d.mu.Lock()
	named, ok := d.resolvers[key]
	d.mu.Unlock()
	if !ok {
		return nil, "", ErrNotFound
	}
	return named.resolver, named.name, nil
}

func (d *Directory) channelKey(channel string) (display, key string, err error) {
	display, key, err = normalizeName(channel)
	if err != nil {
		return "", "", err
	}
	d.mu.Lock()
	named, ok := d.resolvers[key]
	d.mu.Unlock()
	if !ok {
		return "", "", ErrNotFound
	}
	return named.name, key, nil
}

func normalizeName(name string) (display, key string, err error) {
	display = strings.TrimSpace(name)
	if !nameRE.MatchString(display) {
		return "", "", ErrInvalidName
	}
	return display, strings.ToLower(display), nil
}

func externalKey(channelKey, externalID string) string {
	return channelKey + "\x00" + externalID
}

func cloneVars(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}
