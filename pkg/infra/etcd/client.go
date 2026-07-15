package etcd

import (
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// NewClient 按 Config 建立一个 etcd v3 客户端。配置中心、分布式锁与
// pkg/service/discover/etcdv3 共用这一处连接构造,避免各写一遍连接参数。
// DialMS<=0 时用 3s 拨号超时。client 生命周期由调用方管理(负责 Close)。
func NewClient(c *Config) (*clientv3.Client, error) {
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
		return nil, fmt.Errorf("etcd: connect %v: %w", c.Endpoints, err)
	}
	return client, nil
}
