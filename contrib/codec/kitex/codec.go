// Package kitex 提供 Kitex 兼容的服务发现编解码实现。
//
// Kitex 在 etcd 中的注册格式为：
//   - key: {prefix}/{serviceName}{addr}（默认 prefix "kitex/registry-etcd"）
//   - value: JSON instanceInfo 对象
//
// instanceInfo 结构：
//
//	{"network":"tcp","address":"10.0.1.5:8888","weight":100,"tags":{"key":"val"}}
//
// 使用方式：
//
//	import kitexcodec "github.com/rushteam/beauty/contrib/codec/kitex"
//
//	reg := etcdv3.NewRegistry(&etcdv3.Config{
//	    Endpoints: []string{"127.0.0.1:2379"},
//	    Codec:     kitexcodec.NewKVCodec("kitex/registry-etcd"),
//	})
package kitex

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rushteam/beauty/pkg/service/discover"
)

func init() {
	discover.RegisterCodec("kitex", NewCodec())
	discover.RegisterKVCodec("kitex", NewKVCodec("kitex/registry-etcd"))
}

// instanceInfo 对应 kitex-contrib/registry-etcd 的注册值格式。
type instanceInfo struct {
	Network string            `json:"network"`
	Address string            `json:"address"`
	Weight  int               `json:"weight"`
	Tags    map[string]string `json:"tags,omitempty"`
}

// codec 过滤策略：接受所有服务（Kitex 不按 kind 区分）。
// 适用于 Nacos / Consul 等非 KV 后端。
type codec struct{}

// NewCodec 创建 Kitex 兼容的 Codec（Nacos/Consul 场景使用）。
func NewCodec() discover.Codec { return &codec{} }

func (c *codec) Accept(_ discover.ServiceInfo) bool { return true }

// kvCodec 提供 Kitex 兼容的 KV 编解码（etcd 场景使用）。
// key: {prefix}/{serviceName}{addr}    value: instanceInfo JSON
type kvCodec struct {
	prefix string
}

// NewKVCodec 创建 Kitex 兼容的 KVCodec（etcd 场景使用）。
// prefix 对应 Kitex etcd 注册前缀，默认 "kitex/registry-etcd"。
func NewKVCodec(prefix string) discover.KVCodec {
	if prefix == "" {
		prefix = "kitex/registry-etcd"
	}
	return &kvCodec{prefix: prefix}
}

func (c *kvCodec) Accept(_ discover.ServiceInfo) bool { return true }

// BuildKey 构建 Kitex 格式的 etcd key。
// Kitex 原生格式为 {prefix}/{serviceName}{addr}，无分隔符。
func (c *kvCodec) BuildKey(name, id string) string {
	return fmt.Sprintf("%s/%s%s", c.prefix, name, id)
}

// BuildWatchPrefix 构建 Watch 前缀。
func (c *kvCodec) BuildWatchPrefix(name string) string {
	return fmt.Sprintf("%s/%s", c.prefix, name)
}

// MarshalValue 将 beauty Service 序列化为 Kitex instanceInfo JSON。
func (c *kvCodec) MarshalValue(info discover.Service) (string, error) {
	weight := 100
	if md := info.Metadata(); md != nil {
		if w, ok := md["weight"]; ok {
			if v, err := strconv.Atoi(w); err == nil && v > 0 {
				weight = v
			}
		}
	}
	inst := instanceInfo{
		Network: "tcp",
		Address: info.Addr(),
		Weight:  weight,
		Tags:    info.Metadata(),
	}
	data, err := json.Marshal(inst)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UnmarshalValue 将 Kitex instanceInfo JSON 反序列化为 beauty ServiceInfo。
func (c *kvCodec) UnmarshalValue(val []byte, serviceName string) (discover.ServiceInfo, error) {
	var inst instanceInfo
	if err := json.Unmarshal(val, &inst); err != nil {
		return discover.ServiceInfo{}, fmt.Errorf("kitex codec: unmarshal error: %w", err)
	}

	if inst.Address == "" {
		addr := strings.TrimSpace(string(val))
		if addr == "" {
			return discover.ServiceInfo{}, fmt.Errorf("kitex codec: empty address")
		}
		inst.Address = addr
	}

	meta := inst.Tags
	if meta == nil {
		meta = make(map[string]string)
	}
	if inst.Weight > 0 {
		meta["weight"] = strconv.Itoa(inst.Weight)
	}

	return discover.ServiceInfo{
		Kind:     "thrift",
		Name:     serviceName,
		Addr:     inst.Address,
		Metadata: meta,
	}, nil
}
