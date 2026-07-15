package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/pkg/dlock"
)

// 释放/续期都用 Lua 做 CAS(compare-and-set):只有 value 仍等于本次持有的 token 时
// 才删除/续期,防止"锁已过期被他人抢走后,自己误删/误续他人的锁"。
var (
	unlockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0`)

	renewScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
end
return 0`)
)

// DLock 基于 Redis 实现 pkg/dlock 的 Locker 与 Elector。单节点语义见包注释。
//
// Locker(Lock/TryLock)用固定 TTL 的 SET NX 抢占,不自动续期:持有超过 TTL 会
// 被动过期,长任务请设足够大的 TTL,或改用 Elector(它带自动续期,当选期间持续
// 持有,直到续期失败才让位)。这是单节点 Redis 锁的固有取舍,如实标注。
//
// 零值不可用,用 NewDLock / NewDLockFromConfig 构造。
type DLock struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration // 锁/leader key 的存活时间(进程崩溃后最长持有)
	retry  time.Duration // 阻塞 Lock / 竞选时的轮询间隔(Redis 无原生阻塞锁)
}

// DLockOption 配置 DLock。
type DLockOption func(*DLock)

// WithKeyPrefix 设置 key 前缀(默认 "beauty:dlock:"),隔离不同应用/环境。
func WithKeyPrefix(prefix string) DLockOption {
	return func(d *DLock) {
		if prefix != "" {
			d.prefix = prefix
		}
	}
}

// WithTTL 设置锁/leader key 的 TTL(默认 15s)。进程崩溃/卡死后,锁最长在此时长后
// 被动过期释放。Elector 会按 TTL/3 自动续期,故当选期间不受此上限约束。
func WithTTL(ttl time.Duration) DLockOption {
	return func(d *DLock) {
		if ttl > 0 {
			d.ttl = ttl
		}
	}
}

// WithRetryInterval 设置阻塞 Lock / 竞选的轮询间隔(默认 100ms)。
func WithRetryInterval(d time.Duration) DLockOption {
	return func(dl *DLock) {
		if d > 0 {
			dl.retry = d
		}
	}
}

// NewDLock 用已有 Redis 客户端创建 DLock。client 由调用方管理生命周期。
func NewDLock(client redis.UniversalClient, opts ...DLockOption) *DLock {
	d := &DLock{
		client: client,
		prefix: "beauty:dlock:",
		ttl:    15 * time.Second,
		retry:  100 * time.Millisecond,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// NewDLockFromConfig 复用 Config 建连接(并 Ping 校验)后创建 DLock。
func NewDLockFromConfig(c *Config, opts ...DLockOption) (*DLock, error) {
	client, err := pingClient(c)
	if err != nil {
		return nil, fmt.Errorf("redis dlock: %w", err)
	}
	return NewDLock(client, opts...), nil
}

func (d *DLock) key(name string) string { return d.prefix + name }

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("redis dlock: gen token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// TryLock 实现 dlock.Locker:单次 SET NX PX,非阻塞。已被占用返回 (nil, false, nil)。
func (d *DLock) TryLock(ctx context.Context, key string) (dlock.Lock, bool, error) {
	token, err := newToken()
	if err != nil {
		return nil, false, err
	}
	ok, err := d.client.SetNX(ctx, d.key(key), token, d.ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("redis dlock: trylock %s: %w", key, err)
	}
	if !ok {
		return nil, false, nil
	}
	return &redisLock{client: d.client, key: d.key(key), token: token}, true, nil
}

// Lock 实现 dlock.Locker:轮询 SET NX 直到获得锁或 ctx 取消(Redis 无原生阻塞锁)。
func (d *DLock) Lock(ctx context.Context, key string) (dlock.Lock, error) {
	for {
		lock, ok, err := d.TryLock(ctx, key)
		if err != nil {
			return nil, err
		}
		if ok {
			return lock, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d.retry):
		}
	}
}

// startRenew 启动后台续期:每 ttl/3 用 CAS 续期一次,续期失败(出错或 key 已不属于
// 自己)时关闭返回的 lost 通道。stop 停止续期(幂等)。续期用后台 ctx,与获取时的
// ctx 解耦——持有期只由 stop 控制。
func (d *DLock) startRenew(fullKey, token string) (lost <-chan struct{}, stop func()) {
	lostCh := make(chan struct{})
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	interval := d.ttl / 3
	if interval <= 0 {
		interval = time.Second
	}
	ttlMS := strconv.FormatInt(d.ttl.Milliseconds(), 10)

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				rctx, cancel := context.WithTimeout(context.Background(), interval)
				res, err := renewScript.Run(rctx, d.client, []string{fullKey}, token, ttlMS).Int64()
				cancel()
				if err != nil || res == 0 {
					close(lostCh) // 续期失败/已失去 key
					return
				}
			}
		}
	}()

	return lostCh, func() { stopOnce.Do(func() { close(stopCh) }) }
}

// Run 实现 dlock.Elector:轮询抢占 key 参选,当选后自动续期维持 leader 身份,以
// "持有期间"为 leaderCtx 调用 onElected;续期失败或 outer ctx 取消时 leaderCtx
// 被 cancel。onElected 返回后若 outer ctx 仍存活则重新参选。
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
	token, err := newToken()
	if err != nil {
		return err
	}
	fullKey := d.key(key)

	// 阻塞竞选:轮询 SET NX 直到当选或 ctx 取消。
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := d.client.SetNX(ctx, fullKey, token, d.ttl).Result()
		if err != nil {
			return fmt.Errorf("redis elector: campaign %s: %w", key, err)
		}
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.retry):
		}
	}

	lostCh, stopRenew := d.startRenew(fullKey, token)
	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-lostCh: // 失去 leader 身份(续期失败)
			cancel()
		case <-leaderCtx.Done():
		}
	}()

	onElected(leaderCtx)
	stopRenew()

	// 主动让位:CAS 删除自己的 key,让其他候选者尽快接管(best-effort)。
	relCtx, relCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = unlockScript.Run(relCtx, d.client, []string{fullKey}, token).Err()
	relCancel()
	return nil
}

// redisLock 是一次成功获取的锁,Unlock 用 CAS 删除(只删自己持有的)。
type redisLock struct {
	client redis.UniversalClient
	key    string
	token  string
	once   sync.Once
}

// Unlock 实现 dlock.Lock:CAS 删除自己的锁 key(幂等;锁已过期则无操作)。
func (l *redisLock) Unlock(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		if e := unlockScript.Run(ctx, l.client, []string{l.key}, l.token).Err(); e != nil && !errors.Is(e, redis.Nil) {
			err = fmt.Errorf("redis dlock: unlock %s: %w", l.key, e)
		}
	})
	return err
}

var (
	_ dlock.Locker  = (*DLock)(nil)
	_ dlock.Elector = (*DLock)(nil)
	_ dlock.Lock    = (*redisLock)(nil)
)
