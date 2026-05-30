package etcd

import (
	"context"
	"fmt"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
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
func (cc *ConfigCenter) Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error) {
	watchCtx, cancel := context.WithCancel(ctx)

	var watchOpts []clientv3.OpOption
	if strings.HasSuffix(key, "/") {
		watchOpts = append(watchOpts, clientv3.WithPrefix())
	}

	wch := cc.client.Watch(watchCtx, key, watchOpts...)

	go func() {
		for {
			select {
			case <-watchCtx.Done():
				return
			case resp, ok := <-wch:
				if !ok {
					return
				}
				for _, ev := range resp.Events {
					onChange(string(ev.Kv.Key), string(ev.Kv.Value))
				}
			}
		}
	}()

	return cancel, nil
}

// Close 关闭 etcd 连接
func (cc *ConfigCenter) Close() error {
	return cc.client.Close()
}
