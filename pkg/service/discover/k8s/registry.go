package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var instance = make(map[string]*Registry)
var mu sync.Mutex

var (
	_ discover.Registry  = (*Registry)(nil)
	_ discover.Discovery = (*Registry)(nil)
)

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

	client, err := createKubernetesClient(c.Kubeconfig)
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

// createKubernetesClient 创建 Kubernetes 客户端
func createKubernetesClient(kubeconfig string) (kubernetes.Interface, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		// 使用指定的 kubeconfig 文件
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// 使用集群内配置
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return client, nil
}

// Register 注册服务（k8s 环境下通常不需要手动注册，服务通过 k8s Service 资源管理）
func (r *Registry) Register(ctx context.Context, service discover.Service) (context.CancelFunc, error) {
	logger.Info("k8s registry register service",
		slog.String("service_id", service.ID()),
		slog.String("service_name", service.Name()),
		slog.String("addr", service.Addr()))

	// 在 k8s 环境中，服务注册通常由 k8s Service 资源管理
	// 这里返回一个空的取消函数
	return func() {
		logger.Info("k8s registry unregister service",
			slog.String("service_id", service.ID()),
			slog.String("service_name", service.Name()))
	}, nil
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
		endpoints, err := r.client.CoreV1().Endpoints(r.config.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
		if err != nil {
			logger.Error("failed to get endpoints",
				slog.String("service", svc.Name),
				slog.Any("err", err))
			continue
		}

		infos := r.serviceToServiceInfos(&svc, endpoints)
		serviceInfos = append(serviceInfos, infos...)
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
	go r.watchEndpoints(watchCtx, serviceName, notify)

	return nil
}

// watchServices 监听 Service 资源变化
func (r *Registry) watchServices(ctx context.Context, serviceName string, notify discover.Notify) {
	timeout := int64(r.config.WatchTimeout)
	if timeout <= 0 {
		timeout = 30
	}

	watcher, err := r.client.CoreV1().Services(r.config.Namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector:  r.buildLabelSelector(serviceName),
		FieldSelector:  r.buildFieldSelector(),
		TimeoutSeconds: &timeout,
	})
	if err != nil {
		logger.Error("failed to watch services", slog.Any("err", err))
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				logger.Info("service watch channel closed, restarting...")
				time.Sleep(time.Second * 5)
				r.watchServices(ctx, serviceName, notify)
				return
			}

			r.handleServiceEvent(ctx, event, notify)
		}
	}
}

// watchEndpoints 监听 Endpoints 资源变化
func (r *Registry) watchEndpoints(ctx context.Context, serviceName string, notify discover.Notify) {
	timeout := int64(r.config.WatchTimeout)
	if timeout <= 0 {
		timeout = 30
	}

	watcher, err := r.client.CoreV1().Endpoints(r.config.Namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector:  r.buildLabelSelector(serviceName),
		TimeoutSeconds: &timeout,
	})
	if err != nil {
		logger.Error("failed to watch endpoints", slog.Any("err", err))
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				logger.Info("endpoints watch channel closed, restarting...")
				time.Sleep(time.Second * 5)
				r.watchEndpoints(ctx, serviceName, notify)
				return
			}

			r.handleEndpointsEvent(ctx, event, notify)
		}
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

// handleEndpointsEvent 处理 Endpoints 事件
func (r *Registry) handleEndpointsEvent(ctx context.Context, event watch.Event, notify discover.Notify) {
	switch event.Type {
	case watch.Added, watch.Modified, watch.Deleted:
		// 当 Endpoints 发生变化时，重新获取所有服务实例
		services, err := r.Find(ctx, "")
		if err != nil {
			logger.Error("failed to find services after endpoints event", slog.Any("err", err))
			return
		}
		notify(services)
	}
}

// serviceToServiceInfos 将 k8s Service 和 Endpoints 转换为 ServiceInfo
func (r *Registry) serviceToServiceInfos(svc *corev1.Service, endpoints *corev1.Endpoints) []discover.ServiceInfo {
	var serviceInfos []discover.ServiceInfo

	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.Addresses {
			for _, port := range subset.Ports {
				// 如果配置了端口名称，只处理匹配的端口
				if r.config.PortName != "" && port.Name != r.config.PortName {
					continue
				}

				serviceInfo := discover.ServiceInfo{
					ID:   fmt.Sprintf("%s-%s-%d", svc.Name, addr.IP, port.Port),
					Name: svc.Name,
					Kind: "k8s",
					Addr: fmt.Sprintf("%s:%d", addr.IP, port.Port),
					Metadata: map[string]string{
						"namespace":    svc.Namespace,
						"service_type": string(svc.Spec.Type),
						"port_name":    port.Name,
						"protocol":     string(port.Protocol),
					},
				}

				// 添加服务标签到元数据
				for k, v := range svc.Labels {
					serviceInfo.Metadata["label."+k] = v
				}

				// 添加注解到元数据
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
