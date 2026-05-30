package consul

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/consul/api"
)

// Config consul 连接配置
type Config struct {
	Addr       string
	Token      string
	Datacenter string
	Namespace  string
	Partition  string
}

// ConfigCenter 基于 Consul KV 的配置中心，key 为完整 KV 路径。
type ConfigCenter struct {
	client *api.Client
}

var _ interface {
	Get(ctx context.Context, key string) (string, error)
	Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error)
} = (*ConfigCenter)(nil)

// NewConfigCenter 创建 Consul 配置中心
func NewConfigCenter(c *Config) (*ConfigCenter, error) {
	cfg := api.DefaultConfig()
	if c.Addr != "" {
		cfg.Address = c.Addr
	}
	if c.Token != "" {
		cfg.Token = c.Token
	}
	if c.Datacenter != "" {
		cfg.Datacenter = c.Datacenter
	}
	if c.Namespace != "" {
		cfg.Namespace = c.Namespace
	}
	if c.Partition != "" {
		cfg.Partition = c.Partition
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("consul config: %w", err)
	}
	return &ConfigCenter{client: client}, nil
}

// Get 获取 KV 路径的配置值
func (cc *ConfigCenter) Get(ctx context.Context, key string) (string, error) {
	pair, _, err := cc.client.KV().Get(key, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("consul config: get %s: %w", key, err)
	}
	if pair == nil {
		return "", fmt.Errorf("consul config: key %q not found", key)
	}
	return string(pair.Value), nil
}

// Watch 通过 blocking query 监听 KV 变更，ctx 取消时停止。
func (cc *ConfigCenter) Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error) {
	// 先取一次当前 index
	pair, meta, err := cc.client.KV().Get(key, (&api.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("consul config: watch %s: %w", key, err)
	}
	lastIndex := meta.LastIndex
	if pair != nil {
		// 推送初始值
		onChange(key, string(pair.Value))
	}

	watchCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			if watchCtx.Err() != nil {
				return
			}
			p, m, err := cc.client.KV().Get(key, (&api.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  30 * time.Second,
			}).WithContext(watchCtx))
			if err != nil {
				if watchCtx.Err() != nil {
					return
				}
				time.Sleep(time.Second)
				continue
			}
			if m.LastIndex > lastIndex {
				lastIndex = m.LastIndex
				val := ""
				if p != nil {
					val = string(p.Value)
				}
				onChange(key, val)
			}
		}
	}()

	return cancel, nil
}
