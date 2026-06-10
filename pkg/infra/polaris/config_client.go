package polaris

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	watchBaseDelay = 500 * time.Millisecond
	watchMaxDelay  = 30 * time.Second
)

// Config polaris 连接配置
type Config struct {
	Addresses []string
	Namespace string
	Token     string
}

// ConfigCenter 基于 Polaris 的配置中心。
// key 格式为 "fileGroup/fileName"，例如 "DEFAULT_GROUP/application.yaml"。
type ConfigCenter struct {
	api       api.ConfigFileAPI
	namespace string
}

var _ interface {
	Get(ctx context.Context, key string) (string, error)
	Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error)
} = (*ConfigCenter)(nil)

// NewConfigCenter 创建 Polaris 配置中心
func NewConfigCenter(c *Config) (*ConfigCenter, error) {
	cfg := config.NewDefaultConfiguration(c.Addresses)
	if c.Token != "" {
		cfg.Global.ServerConnector.Token = c.Token
	}
	fileAPI, err := api.NewConfigFileAPIByConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("polaris config: init: %w", err)
	}
	ns := c.Namespace
	if ns == "" {
		ns = "default"
	}
	return &ConfigCenter{
		api:       fileAPI,
		namespace: ns,
	}, nil
}

// splitKey 将 "fileGroup/fileName" 拆分为 (fileGroup, fileName)。
// 没有 "/" 时 fileGroup 默认 "default"。
func splitKey(key string) (fileGroup, fileName string) {
	if g, f, ok := strings.Cut(key, "/"); ok {
		return g, f
	}
	return "default", key
}

// Get 获取配置文件内容
func (cc *ConfigCenter) Get(_ context.Context, key string) (string, error) {
	fileGroup, fileName := splitKey(key)
	cf, err := cc.api.FetchConfigFile(&api.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: cc.namespace,
			FileGroup: fileGroup,
			FileName:  fileName,
		},
	})
	if err != nil {
		return "", fmt.Errorf("polaris config: get %s: %w", key, err)
	}
	return cf.GetContent(), nil
}

// Watch 监听配置文件变更，ctx 取消时停止。
func (cc *ConfigCenter) Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error) {
	fileGroup, fileName := splitKey(key)
	cf, err := cc.api.FetchConfigFile(&api.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: cc.namespace,
			FileGroup: fileGroup,
			FileName:  fileName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("polaris config: watch %s: %w", key, err)
	}

	ch := cf.AddChangeListenerWithChannel()
	watchCtx, cancel := context.WithCancel(ctx)

	go func() {
		delay := watchBaseDelay
		for {
			select {
			case <-watchCtx.Done():
				return
			case ev, ok := <-ch:
				if ok {
					delay = watchBaseDelay // 正常收到事件，重置退避
					onChange(key, ev.NewValue)
					continue
				}
				// channel 被关闭（如 SDK 内部连接异常）：退避后重新拉取并注册监听，
				// 否则热加载会在一次抖动后静默失效。
				slog.Warn("polaris config listener channel closed, reconnecting",
					"key", key, "retry_in", delay)
				select {
				case <-watchCtx.Done():
					return
				case <-time.After(delay):
				}
				if delay < watchMaxDelay {
					if delay *= 2; delay > watchMaxDelay {
						delay = watchMaxDelay
					}
				}
				newCf, err := cc.api.FetchConfigFile(&api.GetConfigFileRequest{
					GetConfigFileRequest: &model.GetConfigFileRequest{
						Namespace: cc.namespace,
						FileGroup: fileGroup,
						FileName:  fileName,
					},
				})
				if err != nil {
					slog.Warn("polaris config reconnect: fetch failed", "key", key, "err", err)
					continue
				}
				ch = newCf.AddChangeListenerWithChannel()
				// 补推一次当前值，补齐断连期间可能遗漏的变更
				onChange(key, newCf.GetContent())
			}
		}
	}()

	return cancel, nil
}
