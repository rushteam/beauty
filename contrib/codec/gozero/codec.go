// Package gozero 提供 go-zero 兼容的服务发现编解码实现。
//
// go-zero 在 etcd 中的注册格式为：
//   - key: {serviceName}/{leaseId}
//   - value: host:port（纯文本）
//
// 使用方式：
//
//	import gozercodec "github.com/rushteam/beauty/contrib/codec/gozero"
//
//	reg := etcdv3.NewRegistry(&etcdv3.Config{
//	    Endpoints: []string{"127.0.0.1:2379"},
//	    Codec:     gozerocodec.NewKVCodec(),
//	})
package gozero

import (
	"fmt"
	"strings"

	"github.com/rushteam/beauty/pkg/service/discover"
)

// codec 过滤策略：接受所有服务（go-zero 不按 kind 区分）。
type codec struct{}

// NewCodec 创建 go-zero 兼容的 Codec（Nacos/Consul 场景使用）。
func NewCodec() discover.Codec { return &codec{} }

func (c *codec) Accept(_ discover.ServiceInfo) bool { return true }

// kvCodec 提供 go-zero 兼容的 KV 编解码（etcd 场景使用）。
// key: {name}/{id}    value: host:port 纯字符串
type kvCodec struct {
	codec
}

// NewKVCodec 创建 go-zero 兼容的 KVCodec（etcd 场景使用）。
func NewKVCodec() discover.KVCodec { return &kvCodec{} }

func (c *kvCodec) BuildKey(name, id string) string {
	return fmt.Sprintf("%s/%s", name, id)
}

func (c *kvCodec) BuildWatchPrefix(name string) string {
	return name
}

func (c *kvCodec) MarshalValue(info discover.Service) (string, error) {
	return info.Addr(), nil
}

func (c *kvCodec) UnmarshalValue(val []byte, serviceName string) (discover.ServiceInfo, error) {
	v := discover.ServiceInfo{}
	if err := v.Unmarshal(val); err == nil && (v.Kind != "" || v.Name != "" || v.ID != "") {
		return v, nil
	}
	addr := strings.TrimSpace(string(val))
	if addr == "" {
		return v, fmt.Errorf("empty value")
	}
	return discover.ServiceInfo{
		Kind: "grpc",
		Name: serviceName,
		Addr: addr,
	}, nil
}
