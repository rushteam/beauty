package conf

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// options 控制 Loader 的行为，通过 Option 函数设置。
type options struct {
	strictFields bool
}

// Option 配置 New / Load 的行为。
type Option func(*options)

// WithStrictFields 开启严格字段校验：当配置内容中出现目标结构体里不存在的
// 字段（常见于 key 拼写错误）时，Unmarshal 返回错误而不是静默忽略。
//
//	loader, _ := conf.New("config.yaml", conf.WithStrictFields())
func WithStrictFields() Option {
	return func(o *options) { o.strictFields = true }
}

func buildOptions(opts []Option) options {
	var o options
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}

// decoderOpts 根据是否严格校验返回传给 viper.Unmarshal 的 DecoderConfig 选项。
func decoderOpts(strict bool) []viper.DecoderConfigOption {
	if !strict {
		return nil
	}
	return []viper.DecoderConfigOption{
		func(dc *mapstructure.DecoderConfig) { dc.ErrorUnused = true },
	}
}

// initialLoadTimeout 远程配置首次加载的最长等待时间，避免配置中心不可达时
// New() 永久阻塞导致进程卡在启动阶段（K8s 会在就绪前 evict）。
const initialLoadTimeout = 10 * time.Second

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
	strict bool
}

func (l *fileLoader) Unmarshal(dst any) error {
	return l.Viper.Unmarshal(dst, decoderOpts(l.strict)...)
}

func (l *fileLoader) Watch(ctx context.Context, fn func()) {
	l.Viper.OnConfigChange(func(e fsnotify.Event) {
		if ctx.Err() == nil {
			fn()
		}
	})
	l.Viper.WatchConfig()
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
//
// 密钥与配置分离：YAML 中写占位符，再用 WithSecrets 包装 Loader：
//
//	loader, _ := conf.New("config.yaml")
//	loader = conf.WithSecrets(loader) // ${env:NAME} / ${file:/path}
//	loader.Unmarshal(&cfg)
func New(rawURL string, opts ...Option) (Loader, error) {
	o := buildOptions(opts)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("conf: parse url: %w", err)
	}
	if u.Scheme == "" || u.Scheme == "file" {
		return newFileLoader(u.Path, o)
	}
	return newRemoteLoaderFromURL(u, o)
}

func newFileLoader(path string, o options) (Loader, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType(strings.TrimPrefix(filepath.Ext(path), "."))
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("conf: read file %s: %w", path, err)
	}
	return &fileLoader{Viper: v, strict: o.strictFields}, nil
}

func newRemoteLoaderFromURL(u *url.URL, o options) (Loader, error) {
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
	rl := newRemoteLoader(cc, key, format, o.strictFields)
	ctx, cancel := context.WithTimeout(context.Background(), initialLoadTimeout)
	defer cancel()
	if err := rl.load(ctx); err != nil {
		return nil, fmt.Errorf("conf: initial load key %q: %w", key, err)
	}
	return rl, nil
}
