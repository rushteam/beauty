// Package kratos 提供 Kratos 兼容的服务发现编解码实现。
//
// Kratos 在 etcd 中的注册格式为：
//   - key: /microservices/{name}/{id}  （默认 namespace = "microservices"）
//   - value: JSON 对象，包含 id, name, version, metadata, endpoints 等字段
//
// endpoints 是一个字符串数组，每项为 "scheme://host:port" 格式，
// 例如 ["grpc://10.0.1.5:9000", "http://10.0.1.5:8000"]。
//
// 使用方式：
//
//	import kratoscodec "github.com/rushteam/beauty/contrib/codec/kratos"
//
//	reg := etcdv3.NewRegistry(&etcdv3.Config{
//	    Endpoints: []string{"127.0.0.1:2379"},
//	    Codec:     kratoscodec.NewKVCodec("microservices"),
//	})
package kratos

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/rushteam/beauty/pkg/service/discover"
)

// kratosServiceInstance 对应 Kratos registry.ServiceInstance 的序列化格式。
type kratosServiceInstance struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Version   string            `json:"version,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Endpoints []string          `json:"endpoints,omitempty"`
}

// codec 过滤策略：仅接受包含 grpc:// endpoint 的服务。
// 适用于 Nacos / Consul 等非 KV 后端。
type codec struct{}

// NewCodec 创建 Kratos 兼容的 Codec（非 KV 后端使用）。
func NewCodec() discover.Codec { return &codec{} }

func (c *codec) Accept(info discover.ServiceInfo) bool {
	if info.Kind == "grpc" {
		return true
	}
	return info.Metadata != nil && info.Metadata["kind"] == "grpc"
}

// kvCodec 提供 Kratos 兼容的 KV 编解码。
type kvCodec struct {
	namespace string
}

// NewKVCodec 创建 Kratos 兼容的 KVCodec。
// namespace 对应 Kratos 的注册前缀，默认 "microservices"。
func NewKVCodec(namespace string) discover.KVCodec {
	if namespace == "" {
		namespace = "microservices"
	}
	return &kvCodec{namespace: namespace}
}

func (c *kvCodec) Accept(info discover.ServiceInfo) bool {
	if info.Kind == "grpc" {
		return true
	}
	return info.Metadata != nil && info.Metadata["kind"] == "grpc"
}

func (c *kvCodec) BuildKey(name, id string) string {
	return fmt.Sprintf("/%s/%s/%s", c.namespace, name, id)
}

func (c *kvCodec) BuildWatchPrefix(name string) string {
	return fmt.Sprintf("/%s/%s", c.namespace, name)
}

// MarshalValue 将 beauty Service 序列化为 Kratos 格式的 JSON。
// beauty 每个服务只有一个地址（addr），这里根据 kind 选择 scheme。
func (c *kvCodec) MarshalValue(info discover.Service) (string, error) {
	scheme := "grpc"
	if info.Kind() != "" && info.Kind() != "grpc" {
		scheme = info.Kind()
	}
	inst := kratosServiceInstance{
		ID:        info.ID(),
		Name:      info.Name(),
		Metadata:  info.Metadata(),
		Endpoints: []string{fmt.Sprintf("%s://%s", scheme, info.Addr())},
	}
	if inst.Metadata != nil {
		inst.Version = inst.Metadata["version"]
	}
	data, err := json.Marshal(inst)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UnmarshalValue 将 Kratos 格式的 JSON 反序列化为 beauty ServiceInfo。
// 从 endpoints 中提取第一个 grpc:// 地址；如果没有 grpc，取第一个。
func (c *kvCodec) UnmarshalValue(val []byte, serviceName string) (discover.ServiceInfo, error) {
	var inst kratosServiceInstance
	if err := json.Unmarshal(val, &inst); err != nil {
		return discover.ServiceInfo{}, fmt.Errorf("kratos codec: unmarshal error: %w", err)
	}

	addr, kind := extractEndpoint(inst.Endpoints)
	if addr == "" {
		return discover.ServiceInfo{}, fmt.Errorf("kratos codec: no endpoints in value")
	}

	meta := inst.Metadata
	if meta == nil {
		meta = make(map[string]string)
	}
	if inst.Version != "" {
		meta["version"] = inst.Version
	}

	return discover.ServiceInfo{
		ID:       inst.ID,
		Name:     firstNonEmpty(inst.Name, serviceName),
		Kind:     kind,
		Addr:     addr,
		Metadata: meta,
	}, nil
}

// extractEndpoint 从 Kratos endpoints 列表中提取地址。
// 优先取 grpc:// 开头的 endpoint；如果没有则取第一个。
func extractEndpoint(endpoints []string) (addr, kind string) {
	if len(endpoints) == 0 {
		return "", ""
	}
	for _, ep := range endpoints {
		u, err := url.Parse(ep)
		if err != nil {
			continue
		}
		if strings.EqualFold(u.Scheme, "grpc") {
			return u.Host, "grpc"
		}
	}
	u, err := url.Parse(endpoints[0])
	if err != nil {
		return strings.TrimPrefix(endpoints[0], "//"), "grpc"
	}
	return u.Host, u.Scheme
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
