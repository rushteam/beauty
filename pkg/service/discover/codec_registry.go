package discover

import "sync"

var (
	codecsMu sync.RWMutex
	codecs   = map[string]Codec{}

	kvCodecsMu sync.RWMutex
	kvCodecs   = map[string]KVCodec{}
)

func init() {
	RegisterCodec("beauty", NewBeautyCodec())
	RegisterCodec("accept_all", AcceptAllCodec())
	RegisterKVCodec("beauty", NewBeautyKVCodec("beauty"))
}

// RegisterCodec 注册一个命名的 Codec（用于 nacos/consul 等非 KV 后端）。
// 注册后可通过 URL 参数 ?codec=name 在 NewFromURL 中引用。
// 通常在 init() 中调用。
func RegisterCodec(name string, c Codec) {
	codecsMu.Lock()
	codecs[name] = c
	codecsMu.Unlock()
}

// GetCodec 按名称查找已注册的 Codec。
func GetCodec(name string) (Codec, bool) {
	codecsMu.RLock()
	c, ok := codecs[name]
	codecsMu.RUnlock()
	return c, ok
}

// RegisterKVCodec 注册一个命名的 KVCodec（用于 etcd 等 KV 后端）。
// 注册后可通过 URL 参数 ?codec=name 在 NewFromURL 中引用。
func RegisterKVCodec(name string, c KVCodec) {
	kvCodecsMu.Lock()
	kvCodecs[name] = c
	kvCodecsMu.Unlock()
}

// GetKVCodec 按名称查找已注册的 KVCodec。
func GetKVCodec(name string) (KVCodec, bool) {
	kvCodecsMu.RLock()
	c, ok := kvCodecs[name]
	kvCodecsMu.RUnlock()
	return c, ok
}

// acceptAllCodec 接受所有服务实例，不做任何过滤。
type acceptAllCodec struct{}

// AcceptAllCodec 返回一个接受所有服务实例的 Codec。
func AcceptAllCodec() Codec { return &acceptAllCodec{} }

func (c *acceptAllCodec) Accept(_ ServiceInfo) bool { return true }
