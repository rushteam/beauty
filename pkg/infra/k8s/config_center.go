package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

const (
	watchBaseDelay = 500 * time.Millisecond
	watchMaxDelay  = 30 * time.Second
)

// resourceKind 区分配置载体是 ConfigMap 还是 Secret。
type resourceKind int

const (
	kindConfigMap resourceKind = iota
	kindSecret
)

// ConfigCenter 基于 k8s ConfigMap / Secret 实现 pkg/conf.ConfigCenter,让纯 k8s
// 部署不必额外运维 nacos/etcd 等配置中心——直接把配置塞进 ConfigMap(或敏感项塞
// Secret),改一下资源应用就能热感知。
//
// key 语义:形如 "<name>/<dataKey>",其中 name 是 ConfigMap/Secret 资源名,dataKey
// 是其 .data 里的条目名(通常就是 app.yaml 这样的文件名)。命名空间在构造时固定。
// ConfigMap 读取顺序 Data → BinaryData;Secret 读 Data(client-go 已做 base64 解码)。
//
// Watch 用 client-go 的 Watch(按 metadata.name 字段选择器只盯目标资源)+ 断线指数
// 退避重连实现。k8s 每次(重)建立 watch 会先补发一个当前状态的 ADDED 事件,叠加
// 周期性 watch 超时重连,会重复推同一份内容;这里按值去重(dataKey 的值未变则不
// 回调),避免无谓地触发上层重载。资源被删除时只告警、不回调空值,保留 last-good,
// 免得一次误删/重建就把运行中的配置清空。
//
// 零值不可用,用 NewConfigMapCenter / NewSecretCenter 构造。
type ConfigCenter struct {
	client    kubernetes.Interface
	namespace string
	kind      resourceKind
}

var _ interface {
	Get(ctx context.Context, key string) (string, error)
	Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error)
} = (*ConfigCenter)(nil)

// NewConfigMapCenter 用已有 clientset 创建读 ConfigMap 的配置中心。namespace 为空
// 时用 "default"。client 生命周期由调用方管理。
func NewConfigMapCenter(client kubernetes.Interface, namespace string) *ConfigCenter {
	return newConfigCenter(client, namespace, kindConfigMap)
}

// NewSecretCenter 用已有 clientset 创建读 Secret 的配置中心(用法同 NewConfigMapCenter)。
func NewSecretCenter(client kubernetes.Interface, namespace string) *ConfigCenter {
	return newConfigCenter(client, namespace, kindSecret)
}

func newConfigCenter(client kubernetes.Interface, namespace string, kind resourceKind) *ConfigCenter {
	if namespace == "" {
		namespace = "default"
	}
	return &ConfigCenter{client: client, namespace: namespace, kind: kind}
}

func (cc *ConfigCenter) kindName() string {
	if cc.kind == kindSecret {
		return "secret"
	}
	return "configmap"
}

// splitKey 把 "<name>/<dataKey>" 拆成资源名和数据键。ConfigMap/Secret 的 data 键
// 限定为 [-._a-zA-Z0-9]+(不含 "/"),所以按第一个 "/" 拆分是无歧义的。
func splitKey(key string) (name, dataKey string, err error) {
	i := strings.IndexByte(key, '/')
	if i <= 0 || i == len(key)-1 {
		return "", "", fmt.Errorf("k8s config: key %q must be in form <name>/<dataKey>", key)
	}
	return key[:i], key[i+1:], nil
}

// Get 读取 <name>/<dataKey> 指向的配置值。
func (cc *ConfigCenter) Get(ctx context.Context, key string) (string, error) {
	name, dataKey, err := splitKey(key)
	if err != nil {
		return "", err
	}
	val, ok, err := cc.fetch(ctx, name, dataKey)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("k8s config: %s %q in namespace %q has no data key %q",
			cc.kindName(), name, cc.namespace, dataKey)
	}
	return val, nil
}

func (cc *ConfigCenter) fetch(ctx context.Context, name, dataKey string) (string, bool, error) {
	switch cc.kind {
	case kindSecret:
		s, err := cc.client.CoreV1().Secrets(cc.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", false, fmt.Errorf("k8s config: get secret %s/%s: %w", cc.namespace, name, err)
		}
		if b, ok := s.Data[dataKey]; ok {
			return string(b), true, nil
		}
		return "", false, nil
	default:
		cm, err := cc.client.CoreV1().ConfigMaps(cc.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", false, fmt.Errorf("k8s config: get configmap %s/%s: %w", cc.namespace, name, err)
		}
		return dataFromConfigMap(cm, dataKey)
	}
}

func dataFromConfigMap(cm *corev1.ConfigMap, dataKey string) (string, bool, error) {
	if v, ok := cm.Data[dataKey]; ok {
		return v, true, nil
	}
	if b, ok := cm.BinaryData[dataKey]; ok {
		return string(b), true, nil
	}
	return "", false, nil
}

// Watch 监听目标资源变更,dataKey 的值发生变化时以新值回调 onChange。
// ctx 取消或调用返回的 cancel 均停止监听。
func (cc *ConfigCenter) Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error) {
	name, dataKey, err := splitKey(key)
	if err != nil {
		return nil, err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	go cc.watchLoop(watchCtx, key, name, dataKey, onChange)
	return cancel, nil
}

func (cc *ConfigCenter) watchLoop(ctx context.Context, key, name, dataKey string, onChange func(key, value string)) {
	delay := watchBaseDelay
	var (
		last    string
		hasLast bool
	)
	for {
		if ctx.Err() != nil {
			return
		}
		w, err := cc.watchResource(ctx, name)
		if err != nil {
			slog.Warn("k8s config: establish watch failed, will retry",
				"key", key, "err", err, "retry_in", delay)
			if !sleepOrDone(ctx, delay) {
				return
			}
			delay = nextDelay(delay)
			continue
		}

		healthy := false
		for ev := range w.ResultChan() {
			if ev.Type == watch.Error {
				slog.Warn("k8s config: watch received error event, reconnecting", "key", key)
				break
			}
			// 成功收到首个正常事件即认为本轮连接健康,重置退避。
			if !healthy {
				healthy = true
				delay = watchBaseDelay
			}
			if ev.Type == watch.Deleted {
				slog.Warn("k8s config: source resource deleted, keeping last-good config",
					"key", key, "kind", cc.kindName())
				continue
			}
			if ev.Type != watch.Added && ev.Type != watch.Modified {
				continue // Bookmark 等,忽略
			}
			val, ok := extractEvent(ev.Object, cc.kind, dataKey)
			if !ok {
				continue // 该资源里没有这个 dataKey,当作无变更
			}
			if hasLast && val == last {
				continue // 去重:重连补发的 ADDED、或其它字段变更导致的 MODIFIED
			}
			last, hasLast = val, true
			onChange(key, val)
		}
		w.Stop()

		if ctx.Err() != nil {
			return
		}
		// channel 被 apiserver 关闭(周期性超时)或异常断开:退避后重连。
		slog.Warn("k8s config: watch channel closed, reconnecting", "key", key, "retry_in", delay)
		if !sleepOrDone(ctx, delay) {
			return
		}
		delay = nextDelay(delay)
	}
}

func (cc *ConfigCenter) watchResource(ctx context.Context, name string) (watch.Interface, error) {
	opts := metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
	}
	if cc.kind == kindSecret {
		return cc.client.CoreV1().Secrets(cc.namespace).Watch(ctx, opts)
	}
	return cc.client.CoreV1().ConfigMaps(cc.namespace).Watch(ctx, opts)
}

func extractEvent(obj runtime.Object, kind resourceKind, dataKey string) (string, bool) {
	if kind == kindSecret {
		s, ok := obj.(*corev1.Secret)
		if !ok {
			return "", false
		}
		if b, ok := s.Data[dataKey]; ok {
			return string(b), true
		}
		return "", false
	}
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return "", false
	}
	v, ok, _ := dataFromConfigMap(cm, dataKey)
	return v, ok
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextDelay(d time.Duration) time.Duration {
	if d *= 2; d > watchMaxDelay {
		return watchMaxDelay
	}
	return d
}
