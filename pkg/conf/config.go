package conf

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Loader 统一配置加载接口。
// Unmarshal 将当前配置反序列化到 dst（热加载后再次调用可拿到新值）。
// Watch 在每次配置变更时异步调用 fn，ctx 取消后停止监听。
type Loader interface {
	Unmarshal(dst any) error
	Watch(ctx context.Context, fn func())
}

// fileLoader 基于本地文件 + fsnotify 的实现。
type fileLoader struct {
	*viper.Viper
}

func (l *fileLoader) Unmarshal(dst any) error {
	return l.Viper.Unmarshal(dst)
}

func (l *fileLoader) Watch(ctx context.Context, fn func()) {
	l.Viper.OnConfigChange(func(fsnotify.Event) { fn() })
	l.Viper.WatchConfig()
	go func() { <-ctx.Done() }()
}

// New 根据 rawURL 构造 Loader。
//
//   - 无 scheme 或 scheme == "file"：读取本地文件，支持 fsnotify 热加载。
//   - 其他 scheme（etcd / nacos / consul / polaris …）：
//     需提前 import 对应 infra 子包（触发 init 注册工厂），
//     URL 格式由各工厂决定，key 取自 URL Path（去掉前导 /）。
//
// 远程示例：
//
//	conf.New("etcd://127.0.0.1:2379/myapp/config.yaml")
//	conf.New("nacos://127.0.0.1:8848/myapp.yaml?namespace=dev&group=DEFAULT_GROUP")
//	conf.New("consul://127.0.0.1:8500/myapp/config.yaml")
func New(rawURL string) (Loader, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("conf: parse url: %w", err)
	}
	if u.Scheme == "" || u.Scheme == "file" {
		return newFileLoader(u.Path)
	}
	return newRemoteLoaderFromURL(u)
}

func newFileLoader(path string) (Loader, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType(strings.TrimPrefix(filepath.Ext(path), "."))
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("conf: read file %s: %w", path, err)
	}
	return &fileLoader{v}, nil
}

func newRemoteLoaderFromURL(u *url.URL) (Loader, error) {
	fn, ok := lookupFactory(u.Scheme)
	if !ok {
		return nil, fmt.Errorf("conf: unsupported scheme %q — import the matching infra package to register it", u.Scheme)
	}
	cc, err := fn(u)
	if err != nil {
		return nil, fmt.Errorf("conf: create config center: %w", err)
	}
	// key = URL path（去掉前导 /），query 中可额外传 format=json 覆盖推断
	key := strings.TrimPrefix(u.Path, "/")
	format := u.Query().Get("format")
	if format == "" {
		format = inferFormat(key)
	}
	rl := newRemoteLoader(cc, key, format)
	if err := rl.load(context.Background()); err != nil {
		return nil, fmt.Errorf("conf: initial load key %q: %w", key, err)
	}
	return rl, nil
}
