package discover

import (
	"fmt"
	"strings"
)

// Codec 定义服务发现的过滤策略。
// 注入不同的 Codec 实现可使注册中心兼容不同框架的服务实例。
type Codec interface {
	// Accept 判断发现到的服务实例是否应被接受。
	Accept(info ServiceInfo) bool
}

// KVCodec 扩展 Codec，为 KV 存储型后端（如 etcd）提供键值编解码能力。
// 自定义实现此接口即可对接任意框架的 KV 注册格式。
type KVCodec interface {
	Codec
	// BuildKey 构建注册时的完整 key。
	BuildKey(name, id string) string
	// BuildWatchPrefix 构建发现/监听时的 key 前缀。
	BuildWatchPrefix(name string) string
	// MarshalValue 将服务信息序列化为存储值。
	MarshalValue(info Service) (string, error)
	// UnmarshalValue 将存储值反序列化为 ServiceInfo。
	UnmarshalValue(val []byte, serviceName string) (ServiceInfo, error)
}

// --- beauty 原生格式（core 内置，永远跟随 beauty 本身） ---

// beautyCodec 过滤策略：仅接受 kind="grpc" 的服务。
type beautyCodec struct{}

// NewBeautyCodec 创建 beauty 原生过滤策略的 Codec。
func NewBeautyCodec() Codec { return &beautyCodec{} }

func (c *beautyCodec) Accept(info ServiceInfo) bool {
	if info.Kind == "grpc" {
		return true
	}
	return info.Metadata != nil && info.Metadata["kind"] == "grpc"
}

// beautyKVCodec 提供 beauty 原生 KV 编解码。
// key: /{prefix}/{name}/{id}    value: JSON ServiceInfo
type beautyKVCodec struct {
	beautyCodec
	prefix string
}

// NewBeautyKVCodec 创建 beauty 原生格式的 KVCodec。
// prefix 默认 "beauty"，对应 etcd key 路径 /{prefix}/{name}/{id}。
func NewBeautyKVCodec(prefix string) KVCodec {
	return &beautyKVCodec{prefix: prefix}
}

func (c *beautyKVCodec) BuildKey(name, id string) string {
	return fmt.Sprintf("/%s/%s/%s", strings.TrimPrefix(c.prefix, "/"), name, id)
}

func (c *beautyKVCodec) BuildWatchPrefix(name string) string {
	return fmt.Sprintf("/%s/%s", strings.TrimPrefix(c.prefix, "/"), name)
}

func (c *beautyKVCodec) MarshalValue(info Service) (string, error) {
	v := ServiceInfo{
		ID:       info.ID(),
		Kind:     info.Kind(),
		Name:     info.Name(),
		Addr:     info.Addr(),
		Metadata: info.Metadata(),
	}
	return v.Marshal()
}

func (c *beautyKVCodec) UnmarshalValue(val []byte, _ string) (ServiceInfo, error) {
	v := ServiceInfo{}
	return v, v.Unmarshal(val)
}
