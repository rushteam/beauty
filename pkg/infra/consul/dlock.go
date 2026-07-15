package consul

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"

	"github.com/rushteam/beauty/pkg/dlock"
)

// DLock 基于 Consul session + KV Acquire 实现 pkg/dlock 的 Locker 与 Elector,
// 是 etcd 之外的另一个跨进程后端。不重新发明算法,直接用 Consul 的会话机制:
//
//   - 每次上锁/竞选创建一个带 TTL 的 session(Behavior=release,失效即释放其持有
//     的锁),后台 goroutine 用 Session.RenewPeriodic 持续续期;续期失败(网络分区
//     /进程卡死)即视为"失去 session",这是 leader 身份失效的唯一真相来源。
//   - 互斥锁靠 KV.Acquire 的原子性:同一 key 同时只有一个 session 能 Acquire 成功。
//     TryLock 是单次 Acquire(真非阻塞);Lock 在未获取时用阻塞查询等 key 释放后重试。
//
// LockDelay 设为 0:释放/失效后可被立即抢占,failover 语义对齐 etcd 后端(不引入
// Consul 默认的 15s 锁延迟)。key 前缀默认 "beauty/dlock/"。
//
// 零值不可用,用 NewDLock / NewDLockFromConfig 构造。
type DLock struct {
	client     *api.Client
	prefix     string
	sessionTTL string // Consul TTL 字符串,如 "15s";有效区间 [10s, 86400s]
	identity   string // 写入锁 key 的 value,便于排查当前持有者
}

// DLockOption 配置 DLock。
type DLockOption func(*DLock)

// WithLockKeyPrefix 设置 Consul KV key 前缀(默认 "beauty/dlock/"),隔离不同应用/环境。
func WithLockKeyPrefix(prefix string) DLockOption {
	return func(d *DLock) {
		if prefix != "" {
			d.prefix = prefix
		}
	}
}

// WithLockSessionTTL 设置 session TTL(进程崩溃/网络分区后,锁最长在此时长后被动
// 释放)。Consul 要求 TTL 在 [10s, 86400s],小于 10s 会被抬到 10s。默认 15s。
func WithLockSessionTTL(ttl time.Duration) DLockOption {
	return func(d *DLock) {
		sec := max(int(ttl/time.Second), 10) // Consul 最小 10s
		d.sessionTTL = strconv.Itoa(sec) + "s"
	}
}

// WithLockIdentity 设置写入锁 key 的身份标识(默认取 os.Hostname())。仅用于排查,
// 不参与互斥判定(互斥由 session 保证)。
func WithLockIdentity(id string) DLockOption {
	return func(d *DLock) {
		if id != "" {
			d.identity = id
		}
	}
}

// NewDLock 用已有 Consul 客户端创建 DLock。client 由调用方管理生命周期。
func NewDLock(client *api.Client, opts ...DLockOption) *DLock {
	host, _ := os.Hostname()
	d := &DLock{
		client:     client,
		prefix:     "beauty/dlock/",
		sessionTTL: "15s",
		identity:   host,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// NewDLockFromConfig 复用现有 Config 建立连接后创建 DLock,与 NewConfigCenter
// 共享同一套连接配置约定。
func NewDLockFromConfig(c *Config, opts ...DLockOption) (*DLock, error) {
	client, err := NewClient(c)
	if err != nil {
		return nil, fmt.Errorf("consul dlock: %w", err)
	}
	return NewDLock(client, opts...), nil
}

func (d *DLock) key(name string) string { return d.prefix + name }

// session 是一次上锁/竞选持有的 Consul 会话及其续期状态。
type session struct {
	id     string
	doneCh chan struct{} // 关闭以停止续期(RenewPeriodic 随后销毁 session)
	lostCh chan struct{} // 续期失败(失去 session)时关闭
}

// newSession 创建一个 session 并启动后台续期。续期用后台 ctx:锁一旦持有,其存活期
// 与"获取时传入的 ctx"解耦,只受 doneCh(Unlock)控制。
func (d *DLock) newSession(ctx context.Context) (*session, error) {
	entry := &api.SessionEntry{
		Name:      "beauty-dlock",
		TTL:       d.sessionTTL,
		Behavior:  api.SessionBehaviorRelease,
		LockDelay: 0,
	}
	id, _, err := d.client.Session().Create(entry, (&api.WriteOptions{}).WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	s := &session{id: id, doneCh: make(chan struct{}), lostCh: make(chan struct{})}
	go func() {
		// RenewPeriodic 在 doneCh 关闭时返回 nil、在续期失败时返回非 nil。
		if err := d.client.Session().RenewPeriodic(d.sessionTTL, id, &api.WriteOptions{}, s.doneCh); err != nil {
			close(s.lostCh)
		}
	}()
	return s, nil
}

// discard 释放未交付给调用方的 session(获取失败/出错时清理)。
func (d *DLock) discard(s *session, fullKey string) {
	_, _, _ = d.client.KV().Release(&api.KVPair{Key: fullKey, Session: s.id}, nil)
	close(s.doneCh)
}

func (d *DLock) acquire(ctx context.Context, fullKey string, s *session) (bool, error) {
	ok, _, err := d.client.KV().Acquire(&api.KVPair{
		Key:     fullKey,
		Value:   []byte(d.identity),
		Session: s.id,
	}, (&api.WriteOptions{}).WithContext(ctx))
	return ok, err
}

// TryLock 实现 dlock.Locker:单次原子 Acquire,非阻塞。已被占用返回 (nil, false, nil)。
func (d *DLock) TryLock(ctx context.Context, key string) (dlock.Lock, bool, error) {
	fullKey := d.key(key)
	s, err := d.newSession(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("consul dlock: trylock %s: %w", key, err)
	}
	ok, err := d.acquire(ctx, fullKey, s)
	if err != nil {
		d.discard(s, fullKey)
		return nil, false, fmt.Errorf("consul dlock: trylock %s: %w", key, err)
	}
	if !ok {
		d.discard(s, fullKey)
		return nil, false, nil
	}
	return &consulLock{kv: d.client.KV(), key: fullKey, sess: s}, true, nil
}

// Lock 实现 dlock.Locker:阻塞直到获得 key 的锁,或 ctx 取消。
func (d *DLock) Lock(ctx context.Context, key string) (dlock.Lock, error) {
	fullKey := d.key(key)
	s, err := d.newSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("consul dlock: lock %s: %w", key, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			d.discard(s, fullKey)
			return nil, err
		}
		// session 续期失败则重建,否则会拿着死 session 一直抢不到。
		select {
		case <-s.lostCh:
			d.discard(s, fullKey)
			if s, err = d.newSession(ctx); err != nil {
				return nil, fmt.Errorf("consul dlock: lock %s: %w", key, err)
			}
		default:
		}

		ok, err := d.acquire(ctx, fullKey, s)
		if err != nil {
			d.discard(s, fullKey)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("consul dlock: lock %s: %w", key, err)
		}
		if ok {
			return &consulLock{kv: d.client.KV(), key: fullKey, sess: s}, nil
		}
		if err := d.waitForRelease(ctx, fullKey); err != nil {
			d.discard(s, fullKey)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("consul dlock: lock %s: %w", key, err)
		}
	}
}

// waitForRelease 用 Consul 阻塞查询等待 key 被释放(持有它的 session 变化),避免
// 忙轮询。每次阻塞最多 WaitTime,外层循环负责重试;ctx 取消时立即返回。
func (d *DLock) waitForRelease(ctx context.Context, fullKey string) error {
	kv := d.client.KV()
	pair, meta, err := kv.Get(fullKey, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return err
	}
	if pair == nil || pair.Session == "" {
		return nil // 已无人持有,立即重试 Acquire
	}
	_, _, err = kv.Get(fullKey, (&api.QueryOptions{
		WaitIndex: meta.LastIndex,
		WaitTime:  10 * time.Second,
	}).WithContext(ctx))
	return err
}

// Run 实现 dlock.Elector:阻塞竞选 key,拿到锁即当选,以"持有 session 存活期间"为
// leaderCtx 调用 onElected。session 失效(续期失败/网络分区)或 outer ctx 取消时
// leaderCtx 被 cancel;onElected 返回后若 outer ctx 仍存活则重新参选。
func (d *DLock) Run(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := d.electOnce(ctx, key, onElected); err != nil {
			return err
		}
	}
}

func (d *DLock) electOnce(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	lock, err := d.Lock(ctx, key)
	if err != nil {
		return err // ctx.Err() 或真实错误,交给 Run 决定是否退出
	}
	cl := lock.(*consulLock)

	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-cl.sess.lostCh: // 失去 session = 失去 leader 身份
			cancel()
		case <-leaderCtx.Done():
		}
	}()

	onElected(leaderCtx)
	// 主动让位(best-effort):leaderCtx 此时通常已取消,用独立的短超时 ctx 释放。
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = lock.Unlock(releaseCtx)
	releaseCancel()
	return nil
}

// consulLock 是一次成功获取的锁,Unlock 释放并停止 session 续期。
type consulLock struct {
	kv   *api.KV
	key  string
	sess *session
	once sync.Once
}

// Unlock 实现 dlock.Lock:显式释放锁并停止续期(幂等)。
func (l *consulLock) Unlock(ctx context.Context) error {
	l.once.Do(func() {
		_, _, _ = l.kv.Release(&api.KVPair{Key: l.key, Session: l.sess.id}, (&api.WriteOptions{}).WithContext(ctx))
		close(l.sess.doneCh)
	})
	return nil
}

var (
	_ dlock.Locker  = (*DLock)(nil)
	_ dlock.Elector = (*DLock)(nil)
	_ dlock.Lock    = (*consulLock)(nil)
)
