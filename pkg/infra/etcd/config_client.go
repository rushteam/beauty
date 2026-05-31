package etcd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	watchBaseDelay = 500 * time.Millisecond
	watchMaxDelay  = 30 * time.Second
)

// Config etcd 连接配置
type Config struct {
	Endpoints []string
	Username  string
	Password  string
	DialMS    int
}

// ConfigCenter 基于 etcd 的配置中心，key 为完整 etcd 路径。
type ConfigCenter struct {
	client *clientv3.Client
}

var _ interface {
	Get(ctx context.Context, key string) (string, error)
	Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error)
} = (*ConfigCenter)(nil)

// NewConfigCenter 创建 etcd 配置中心
func NewConfigCenter(c *Config) (*ConfigCenter, error) {
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
		return nil, fmt.Errorf("etcd config: %w", err)
	}
	return &ConfigCenter{client: client}, nil
}

// Get 获取 key 的配置值
func (cc *ConfigCenter) Get(ctx context.Context, key string) (string, error) {
	resp, err := cc.client.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("etcd config: get %s: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return "", fmt.Errorf("etcd config: key %q not found", key)
	}
	return string(resp.Kvs[0].Value), nil
}

// Watch 监听 key 变更（支持前缀监听，key 以 "/" 结尾时视为前缀）。
// 删除事件 value 为空字符串。
// 断线后自动重连，指数退避最长 30s，重连成功后推送一次当前值补齐断线期间的变更。
func (cc *ConfigCenter) Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error) {
	watchCtx, cancel := context.WithCancel(ctx)

	var watchOpts []clientv3.OpOption
	if strings.HasSuffix(key, "/") {
		watchOpts = append(watchOpts, clientv3.WithPrefix())
	}

	go cc.watchLoop(watchCtx, key, watchOpts, onChange)

	return cancel, nil
}

// watchLoop 持续监听，channel 关闭或收到错误时指数退避重连。
func (cc *ConfigCenter) watchLoop(ctx context.Context, key string, watchOpts []clientv3.OpOption, onChange func(key, value string)) {
	delay := watchBaseDelay

	for {
		if ctx.Err() != nil {
			return
		}

		wch := cc.client.Watch(ctx, key, watchOpts...)
		healthy := false

		for resp := range wch {
			if err := resp.Err(); err != nil {
				slog.Warn("etcd watch error, will reconnect",
					"key", key, "err", err, "retry_in", delay)
				break
			}
			// 收到第一个正常响应，重置退避计数
			if !healthy {
				healthy = true
				delay = watchBaseDelay
			}
			for _, ev := range resp.Events {
				onChange(string(ev.Kv.Key), string(ev.Kv.Value))
			}
		}

		// channel 因 ctx 取消而关闭，正常退出
		if ctx.Err() != nil {
			return
		}

		// 异常断线：退避等待后重连
		slog.Warn("etcd watch channel closed unexpectedly, reconnecting",
			"key", key, "retry_in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < watchMaxDelay {
			delay *= 2
			if delay > watchMaxDelay {
				delay = watchMaxDelay
			}
		}

		// 重连后补推当前值，避免断线期间的变更被遗漏
		cc.pushCurrent(ctx, key, watchOpts, onChange)
	}
}

// pushCurrent 拉取 key 当前值并回调，断线重连后调用以补齐遗漏的变更。
func (cc *ConfigCenter) pushCurrent(ctx context.Context, key string, watchOpts []clientv3.OpOption, onChange func(key, value string)) {
	// watchOpts 里可能含 WithPrefix，Get 同样支持，直接复用
	resp, err := cc.client.Get(ctx, key, watchOpts...)
	if err != nil {
		slog.Warn("etcd watch reconnect: failed to fetch current value",
			"key", key, "err", err)
		return
	}
	for _, kv := range resp.Kvs {
		onChange(string(kv.Key), string(kv.Value))
	}
}

// Close 关闭 etcd 连接
func (cc *ConfigCenter) Close() error {
	return cc.client.Close()
}
