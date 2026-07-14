package etcd

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"

	"github.com/rushteam/beauty/pkg/dlock"
)

// DLock 基于 etcd 官方 client/v3/concurrency 包(Session + Mutex/Election)实现
// pkg/dlock 的 Locker 与 Elector,是跨进程场景下的真实后端(区别于 dlock.Memory
// 的单进程模拟)。不重新发明分布式锁/选主算法,只是薄封装。
//
// 每次 Lock/TryLock/Run 都会建立一个新的 concurrency.Session(带独立 lease),
// key 前缀默认 "/beauty/dlock/"。Session 的租约 TTL 决定"进程崩溃后锁最长
// 持有多久才被动释放"——默认 10s,可用 WithSessionTTL 调整。
//
// 零值不可用,用 NewDLock 构造。
type DLock struct {
	client *clientv3.Client
	prefix string
	ttlSec int
}

// DLockOption 配置 DLock。
type DLockOption func(*DLock)

// WithSessionTTL 设置 etcd session 租约 TTL(秒,默认 10)。进程崩溃/网络分区后,
// 锁/leader 身份最长在此时长后被动释放(未主动 Unlock/Resign 时的兜底)。
func WithSessionTTL(sec int) DLockOption {
	return func(d *DLock) {
		if sec > 0 {
			d.ttlSec = sec
		}
	}
}

// WithKeyPrefix 设置 etcd key 前缀(默认 "/beauty/dlock/"),隔离不同应用/环境。
func WithKeyPrefix(prefix string) DLockOption {
	return func(d *DLock) {
		if prefix != "" {
			d.prefix = prefix
		}
	}
}

// NewDLock 用已有 etcd 客户端创建 DLock。client 由调用方管理生命周期
// (DLock 不负责 Close 它)。
func NewDLock(client *clientv3.Client, opts ...DLockOption) *DLock {
	d := &DLock{client: client, prefix: "/beauty/dlock/", ttlSec: 10}
	for _, o := range opts {
		o(d)
	}
	return d
}

// NewDLockFromConfig 复用现有 Config 建立连接后创建 DLock,便于和
// NewConfigCenter 共享同一套连接配置约定。
func NewDLockFromConfig(c *Config, opts ...DLockOption) (*DLock, error) {
	dialTimeout := time.Duration(c.DialMS) * time.Millisecond
	if dialTimeout <= 0 {
		dialTimeout = 3 * time.Second
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   c.Endpoints,
		DialTimeout: dialTimeout,
		Username:    c.Username,
		Password:    c.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd dlock: %w", err)
	}
	return NewDLock(client, opts...), nil
}

func (d *DLock) key(name string) string { return d.prefix + name }

// etcdLock 包装一个 Session + Mutex,Unlock 时释放两者。
type etcdLock struct {
	session *concurrency.Session
	mutex   *concurrency.Mutex
}

func (l *etcdLock) Unlock(ctx context.Context) error {
	err := l.mutex.Unlock(ctx)
	l.session.Close() // 释放 lease,幂等
	return err
}

// Lock 实现 dlock.Locker:阻塞直到获得 key 的锁,或 ctx 取消。
func (d *DLock) Lock(ctx context.Context, key string) (dlock.Lock, error) {
	session, err := concurrency.NewSession(d.client, concurrency.WithContext(ctx), concurrency.WithTTL(d.ttlSec))
	if err != nil {
		return nil, fmt.Errorf("etcd dlock: new session: %w", err)
	}
	mutex := concurrency.NewMutex(session, d.key(key))
	if err := mutex.Lock(ctx); err != nil {
		session.Close()
		return nil, fmt.Errorf("etcd dlock: lock %s: %w", key, err)
	}
	return &etcdLock{session: session, mutex: mutex}, nil
}

// TryLock 实现 dlock.Locker:非阻塞尝试获取。
func (d *DLock) TryLock(ctx context.Context, key string) (dlock.Lock, bool, error) {
	session, err := concurrency.NewSession(d.client, concurrency.WithContext(ctx), concurrency.WithTTL(d.ttlSec))
	if err != nil {
		return nil, false, fmt.Errorf("etcd dlock: new session: %w", err)
	}
	mutex := concurrency.NewMutex(session, d.key(key))
	if err := mutex.TryLock(ctx); err != nil {
		session.Close()
		if err == concurrency.ErrLocked {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("etcd dlock: trylock %s: %w", key, err)
	}
	return &etcdLock{session: session, mutex: mutex}, true, nil
}

// Run 实现 dlock.Elector:用 etcd Election 持续参选,当选时以"session 存活期间"
// 为 leaderCtx 调用 onElected。session 因租约失效/网络分区/主动 Close 而结束时,
// leaderCtx 被 cancel——这是 leader 身份失效的唯一真相来源。
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
	session, err := concurrency.NewSession(d.client, concurrency.WithContext(ctx), concurrency.WithTTL(d.ttlSec))
	if err != nil {
		return fmt.Errorf("etcd dlock: new session: %w", err)
	}
	defer session.Close()

	election := concurrency.NewElection(session, d.key(key))
	if err := election.Campaign(ctx, ""); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("etcd dlock: campaign %s: %w", key, err)
	}

	// leaderCtx 的唯一取消来源是 session 结束(outer ctx 取消也会级联到 session,
	// 因为 session 是 WithContext(ctx) 派生的)。
	leaderCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-session.Done():
			cancel()
		case <-leaderCtx.Done():
		}
	}()
	defer cancel()

	onElected(leaderCtx)
	// 主动放弃 leader 身份(best-effort,session.Close 的 defer 兜底)。
	resignCtx, resignCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = election.Resign(resignCtx)
	resignCancel()
	return nil
}

var (
	_ dlock.Locker  = (*DLock)(nil)
	_ dlock.Elector = (*DLock)(nil)
)
