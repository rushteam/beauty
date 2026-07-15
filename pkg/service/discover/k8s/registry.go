package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	beautyk8s "github.com/rushteam/beauty/pkg/infra/k8s"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

var instance = make(map[string]*Registry)
var mu sync.Mutex

var _ discover.RegistryDiscovery = (*Registry)(nil)

// Registry k8s 服务注册和发现实现
type Registry struct {
	config    *Config
	client    kubernetes.Interface
	watchers  map[string]context.CancelFunc
	watcherMu sync.RWMutex
}

// NewRegistry 创建新的 k8s registry
func NewRegistry(c *Config) *Registry {
	key := c.String()
	mu.Lock()
	defer mu.Unlock()

	if v, ok := instance[key]; ok {
		return v
	}

	// 复用 pkg/infra/k8s 的 clientset 构造,与配置中心/选主共用同一处 in-cluster
	// / kubeconfig 判定。
	client, err := beautyk8s.NewClientset(c.Kubeconfig)
	if err != nil {
		logger.Error("k8s registry create client error", slog.Any("err", err))
		return nil
	}

	r := &Registry{
		config:   c,
		client:   client,
		watchers: make(map[string]context.CancelFunc),
	}

	instance[key] = r
	return r
}

// Register 在 k8s 环境中无需手动注册服务，服务生命周期由 k8s Service 资源管理。
func (r *Registry) Register(_ context.Context, _ discover.Service) (context.CancelFunc, error) {
	return func() {}, fmt.Errorf("k8s registry does not support manual service registration; use Kubernetes Service resources instead")
}

// Find 查找服务实例
func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	services, err := r.client.CoreV1().Services(r.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: r.buildLabelSelector(serviceName),
		FieldSelector: r.buildFieldSelector(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	var serviceInfos []discover.ServiceInfo

	for _, svc := range services.Items {
		slices, err := r.client.DiscoveryV1().EndpointSlices(r.config.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "kubernetes.io/service-name=" + svc.Name,
		})
		if err != nil {
			logger.Error("failed to get endpoint slices",
				slog.String("service", svc.Name),
				slog.Any("err", err))
			continue
		}

		for i := range slices.Items {
			infos := r.endpointSliceToServiceInfos(&svc, &slices.Items[i])
			serviceInfos = append(serviceInfos, infos...)
		}
	}

	return serviceInfos, nil
}

// Watch 监听服务变化
func (r *Registry) Watch(ctx context.Context, serviceName string, notify discover.Notify) error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	// 如果已经有监听器在运行，先取消它
	if cancel, exists := r.watchers[serviceName]; exists {
		cancel()
	}

	watchCtx, cancel := context.WithCancel(ctx)
	r.watchers[serviceName] = cancel

	go r.watchServices(watchCtx, serviceName, notify)
	go r.watchEndpointSlices(watchCtx, serviceName, notify)

	return nil
}

// watchServices 监听 Service 资源变化。
// watcher channel 因 k8s 周期性超时（默认 30s）或异常断开时，在外层循环重建，
// 而不是递归自调——递归会让 goroutine 栈帧随重连次数无限累积。
func (r *Registry) watchServices(ctx context.Context, serviceName string, notify discover.Notify) {
	timeout := int64(r.config.WatchTimeout)
	if timeout <= 0 {
		timeout = 30
	}

	for {
		if ctx.Err() != nil {
			return
		}
		watcher, err := r.client.CoreV1().Services(r.config.Namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector:  r.buildLabelSelector(serviceName),
			FieldSelector:  r.buildFieldSelector(),
			TimeoutSeconds: &timeout,
		})
		if err != nil {
			logger.Error("failed to watch services", slog.Any("err", err))
			if !sleepOrDone(ctx, time.Second*5) {
				return
			}
			continue
		}

		r.consumeEvents(ctx, watcher, r.handleServiceEvent, notify)

		if ctx.Err() != nil {
			return
		}
		logger.Info("service watch channel closed, restarting...")
		if !sleepOrDone(ctx, time.Second*5) {
			return
		}
	}
}

// watchEndpointSlices 监听 EndpointSlice 资源变化（重连策略同 watchServices）。
func (r *Registry) watchEndpointSlices(ctx context.Context, serviceName string, notify discover.Notify) {
	timeout := int64(r.config.WatchTimeout)
	if timeout <= 0 {
		timeout = 30
	}

	for {
		if ctx.Err() != nil {
			return
		}
		watcher, err := r.client.DiscoveryV1().EndpointSlices(r.config.Namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector:  r.buildLabelSelector(serviceName),
			TimeoutSeconds: &timeout,
		})
		if err != nil {
			logger.Error("failed to watch endpoint slices", slog.Any("err", err))
			if !sleepOrDone(ctx, time.Second*5) {
				return
			}
			continue
		}

		r.consumeEvents(ctx, watcher, r.handleEndpointSlicesEvent, notify)

		if ctx.Err() != nil {
			return
		}
		logger.Info("endpoint slices watch channel closed, restarting...")
		if !sleepOrDone(ctx, time.Second*5) {
			return
		}
	}
}

// consumeEvents 消费一个 watcher 的事件，channel 关闭或 ctx 取消时返回，
// 并确保 watcher 被释放。
func (r *Registry) consumeEvents(ctx context.Context, watcher watch.Interface,
	handle func(context.Context, watch.Event, discover.Notify), notify discover.Notify) {
	defer watcher.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}
			handle(ctx, event, notify)
		}
	}
}

// sleepOrDone 等待 d 或 ctx 取消，返回 true 表示正常等待结束、可继续。
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// handleServiceEvent 处理 Service 事件
func (r *Registry) handleServiceEvent(ctx context.Context, event watch.Event, notify discover.Notify) {
	switch event.Type {
	case watch.Added, watch.Modified, watch.Deleted:
		// 当 Service 发生变化时，重新获取所有服务实例
		services, err := r.Find(ctx, "")
		if err != nil {
			logger.Error("failed to find services after event", slog.Any("err", err))
			return
		}
		notify(services)
	}
}

// handleEndpointSlicesEvent 处理 EndpointSlice 事件
func (r *Registry) handleEndpointSlicesEvent(ctx context.Context, event watch.Event, notify discover.Notify) {
	switch event.Type {
	case watch.Added, watch.Modified, watch.Deleted:
		services, err := r.Find(ctx, "")
		if err != nil {
			logger.Error("failed to find services after endpoint slices event", slog.Any("err", err))
			return
		}
		notify(services)
	}
}

// endpointSliceToServiceInfos 将 k8s Service 和 EndpointSlice 转换为 ServiceInfo
func (r *Registry) endpointSliceToServiceInfos(svc *corev1.Service, eps *discoveryv1.EndpointSlice) []discover.ServiceInfo {
	var serviceInfos []discover.ServiceInfo

	for _, endpoint := range eps.Endpoints {
		if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
			continue
		}
		for _, address := range endpoint.Addresses {
			for _, port := range eps.Ports {
				if port.Port == nil {
					continue
				}
				if r.config.PortName != "" && (port.Name == nil || *port.Name != r.config.PortName) {
					continue
				}

				portName := ""
				if port.Name != nil {
					portName = *port.Name
				}
				protocol := ""
				if port.Protocol != nil {
					protocol = string(*port.Protocol)
				}

				serviceInfo := discover.ServiceInfo{
					ID:   fmt.Sprintf("%s-%s-%d", svc.Name, address, *port.Port),
					Name: svc.Name,
					Kind: "k8s",
					Addr: fmt.Sprintf("%s:%d", address, *port.Port),
					Metadata: map[string]string{
						"namespace":    svc.Namespace,
						"service_type": string(svc.Spec.Type),
						"port_name":    portName,
						"protocol":     protocol,
					},
				}

				for k, v := range svc.Labels {
					serviceInfo.Metadata["label."+k] = v
				}
				for k, v := range svc.Annotations {
					serviceInfo.Metadata["annotation."+k] = v
				}

				serviceInfos = append(serviceInfos, serviceInfo)
			}
		}
	}

	return serviceInfos
}

// buildLabelSelector 构建标签选择器
func (r *Registry) buildLabelSelector(serviceName string) string {
	selector := r.config.LabelSelector

	// 如果指定了服务名称，添加到选择器中
	if serviceName != "" {
		if selector != "" {
			selector += ","
		}
		// 可以根据需要调整标签选择逻辑
		selector += "app=" + serviceName
	}

	return selector
}

// buildFieldSelector 构建字段选择器
func (r *Registry) buildFieldSelector() string {
	var selectors []string

	// 根据服务类型过滤
	if r.config.ServiceType != "" && r.config.ServiceType != "All" {
		selectors = append(selectors, "spec.type="+r.config.ServiceType)
	}

	if len(selectors) == 0 {
		return ""
	}

	return fields.AndSelectors(
		fields.ParseSelectorOrDie(selectors[0]),
	).String()
}

// Close 关闭所有监听器
func (r *Registry) Close() error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	for serviceName, cancel := range r.watchers {
		cancel()
		delete(r.watchers, serviceName)
	}

	return nil
}
