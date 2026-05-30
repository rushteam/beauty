package conf

import "context"

// ConfigCenter 统一配置中心接口，nacos/etcd/polaris 均实现此接口。
// key 的含义由实现决定：nacos=dataID，etcd=完整路径，polaris=文件名。
type ConfigCenter interface {
	// Get 获取配置内容
	Get(ctx context.Context, key string) (string, error)
	// Watch 监听 key 变更，变更时以新值回调 onChange。
	// ctx 取消或调用返回的 cancel 均可停止监听。
	Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error)
}
