package k8s

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	beautyk8s "github.com/rushteam/beauty/pkg/infra/k8s"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var instance = make(map[string]*Registry)
var mu sync.Mutex

var _ discover.RegistryDiscovery = (*Registry)(nil)

// Registry k8s 服务注册和发现实现
type Registry struct {
	config *Config
	client kubernetes.Interface
	// watchers 按每次 Watch 调用分配的 id 记录 cancel，仅供 Close 统一取消。
	// Registry 按配置单例复用，同一服务可能被多个调用方并发 Watch，不能按服务名去重。
	watchers   map[uint64]context.CancelFunc
	watcherSeq uint64
	watcherMu  sync.RWMutex
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
		watchers: make(map[uint64]context.CancelFunc),
	}

	instance[key] = r
	return r
}

// Register 在 k8s 环境中无需手动注册服务，服务生命周期由 k8s Service 资源管理。
func (r *Registry) Register(_ context.Context, _ discover.Service) (context.CancelFunc, error) {
	return func() {}, fmt.Errorf("k8s registry does not support manual service registration; use Kubernetes Service resources instead")
}

// normalizeServiceName 从 K8s DNS 风格的名称中提取纯服务名。
// "payment-internal.mall" → "payment-internal"
// "payment-internal.mall.svc.cluster.local" → "payment-internal"
// "payment-internal" → "payment-internal"（无 "." 时原样返回）
//
// K8s 资源名遵循 RFC 1123（小写字母、数字、连字符），不含 "."，
// 因此按 "." 切分不会误伤。
func normalizeServiceName(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		name = name[:i]
	}
	if name == "*" {
		return ""
	}
	return name
}

// Find 查找服务实例。固定两次 List：Service 一次、EndpointSlice 一次（按 Service 分组）。
func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
	serviceName = normalizeServiceName(serviceName)

	services, err := r.client.CoreV1().Services(r.config.Namespace).List(ctx, r.serviceListOptions(serviceName))
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}
	if len(services.Items) == 0 {
		return nil, nil
	}
	sliceList, err := r.client.DiscoveryV1().EndpointSlices(r.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: endpointSliceSelector(serviceName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list endpoint slices: %w", err)
	}

	svcs := make([]*corev1.Service, len(services.Items))
	for i := range services.Items {
		svcs[i] = &services.Items[i]
	}
	epss := make([]*discoveryv1.EndpointSlice, len(sliceList.Items))
	for i := range sliceList.Items {
		epss[i] = &sliceList.Items[i]
	}
	return r.buildServiceInfos(svcs, epss), nil
}

// Watch 监听服务变化。阻塞直到 ctx 取消或 Close() 被调用，
// 与 etcd/consul 等后端保持相同的阻塞契约。
//
// 基于 informer：断线/超时后从上次 ResourceVersion 续订（410 时自动重新 List），
// 不会像裸 Watch 那样每次重连都重放全部对象；事件只用于触发刷新，
// 刷新从本地缓存计算、不访问 API server，多个事件合并为一次，结果未变化时不通知。
func (r *Registry) Watch(ctx context.Context, serviceName string, notify discover.Notify) error {
	serviceName = normalizeServiceName(serviceName)

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	r.watcherMu.Lock()
	r.watcherSeq++
	id := r.watcherSeq
	r.watchers[id] = cancel
	r.watcherMu.Unlock()
	defer func() {
		r.watcherMu.Lock()
		delete(r.watchers, id)
		r.watcherMu.Unlock()
	}()

	kick := make(chan struct{}, 1)
	trigger := func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { trigger() },
		UpdateFunc: func(any, any) { trigger() },
		DeleteFunc: func(any) { trigger() },
	}

	svcInf := cache.NewSharedIndexInformerWithOptions(
		r.listWatch(serviceName, r.listServices, r.watchServices), &corev1.Service{}, cache.SharedIndexInformerOptions{})
	sliceInf := cache.NewSharedIndexInformerWithOptions(
		r.listWatch(serviceName, r.listEndpointSlices, r.watchEndpointSlices), &discoveryv1.EndpointSlice{}, cache.SharedIndexInformerOptions{})
	for _, inf := range []cache.SharedIndexInformer{svcInf, sliceInf} {
		_ = inf.SetWatchErrorHandlerWithContext(watchErrorHandler(serviceName))
		_, _ = inf.AddEventHandler(handler)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svcInf.RunWithContext(watchCtx)
	}()
	go func() {
		defer wg.Done()
		sliceInf.RunWithContext(watchCtx)
	}()
	defer func() {
		cancel()
		wg.Wait()
	}()

	// cache.WaitForCacheSync 固定 100ms 轮询，会拖慢 resolver 首次出地址。
	if err := wait.PollUntilContextCancel(watchCtx, 10*time.Millisecond, true, func(context.Context) (bool, error) {
		return svcInf.HasSynced() && sliceInf.HasSynced(), nil
	}); err != nil {
		return nil
	}

	var last []discover.ServiceInfo
	for first := true; ; first = false {
		cur := r.buildServiceInfos(storeList[*corev1.Service](svcInf.GetStore()),
			storeList[*discoveryv1.EndpointSlice](sliceInf.GetStore()))
		if first || !slices.EqualFunc(last, cur, discover.ServiceInfo.Equal) {
			notify(cur)
			last = cur
		}
		select {
		case <-watchCtx.Done():
			return nil
		case <-kick:
		}
	}
}

type listFunc func(ctx context.Context, serviceName string, opts metav1.ListOptions) (runtime.Object, error)
type watchFunc func(ctx context.Context, serviceName string, opts metav1.ListOptions) (watch.Interface, error)

// listWatch 包装 informer 用的 ListerWatcher；配置了 WatchTimeout 时覆盖 reflector 默认的
// 5~10 分钟超时（续订基于 ResourceVersion，超时重连只是一次轻量 Watch）。
func (r *Registry) listWatch(serviceName string, list listFunc, w watchFunc) *cache.ListWatch {
	return &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			return list(ctx, serviceName, opts)
		},
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			if r.config.WatchTimeout > 0 {
				timeout := int64(r.config.WatchTimeout)
				opts.TimeoutSeconds = &timeout
			}
			return w(ctx, serviceName, opts)
		},
	}
}

func (r *Registry) listServices(ctx context.Context, serviceName string, opts metav1.ListOptions) (runtime.Object, error) {
	sel := r.serviceListOptions(serviceName)
	opts.LabelSelector, opts.FieldSelector = sel.LabelSelector, sel.FieldSelector
	return r.client.CoreV1().Services(r.config.Namespace).List(ctx, opts)
}

func (r *Registry) watchServices(ctx context.Context, serviceName string, opts metav1.ListOptions) (watch.Interface, error) {
	sel := r.serviceListOptions(serviceName)
	opts.LabelSelector, opts.FieldSelector = sel.LabelSelector, sel.FieldSelector
	return r.client.CoreV1().Services(r.config.Namespace).Watch(ctx, opts)
}

func (r *Registry) listEndpointSlices(ctx context.Context, serviceName string, opts metav1.ListOptions) (runtime.Object, error) {
	opts.LabelSelector = endpointSliceSelector(serviceName)
	return r.client.DiscoveryV1().EndpointSlices(r.config.Namespace).List(ctx, opts)
}

func (r *Registry) watchEndpointSlices(ctx context.Context, serviceName string, opts metav1.ListOptions) (watch.Interface, error) {
	opts.LabelSelector = endpointSliceSelector(serviceName)
	return r.client.DiscoveryV1().EndpointSlices(r.config.Namespace).Watch(ctx, opts)
}

// watchErrorHandler 把 reflector 的 List/Watch 错误转到 slog。
// 正常的超时关闭（EOF）与 410 过期由 reflector 自行续订/重新 List，只记 Debug。
func watchErrorHandler(serviceName string) cache.WatchErrorHandlerWithContext {
	return func(ctx context.Context, _ *cache.Reflector, err error) {
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
			apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
			logger.Debug("k8s watch restarting", slog.String("service", serviceName), slog.Any("err", err))
			return
		}
		logger.Warn("k8s watch error", slog.String("service", serviceName), slog.Any("err", err))
	}
}

func storeList[T any](s cache.Store) []T {
	objs := s.List()
	out := make([]T, 0, len(objs))
	for _, o := range objs {
		if v, ok := o.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// buildServiceInfos 按 namespace/name 把 EndpointSlice 归到所属 Service 下展开为实例，
// Service、EndpointSlice 均按名称排序，保证输出稳定、可比较。
// 不属于 svcs 的 EndpointSlice 被忽略，Service 的 label/type 过滤因此同样作用于端点。
func (r *Registry) buildServiceInfos(svcs []*corev1.Service, epss []*discoveryv1.EndpointSlice) []discover.ServiceInfo {
	if len(svcs) == 0 {
		return nil
	}
	bySvc := make(map[string][]*discoveryv1.EndpointSlice, len(svcs))
	for _, eps := range epss {
		name, ok := eps.Labels[discoveryv1.LabelServiceName]
		if !ok {
			continue
		}
		key := eps.Namespace + "/" + name
		bySvc[key] = append(bySvc[key], eps)
	}
	slices.SortFunc(svcs, func(a, b *corev1.Service) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})

	var serviceInfos []discover.ServiceInfo
	for _, svc := range svcs {
		group := bySvc[svc.Namespace+"/"+svc.Name]
		slices.SortFunc(group, func(a, b *discoveryv1.EndpointSlice) int {
			return cmp.Compare(a.Name, b.Name)
		})
		for _, eps := range group {
			serviceInfos = append(serviceInfos, r.endpointSliceToServiceInfos(svc, eps)...)
		}
	}
	return serviceInfos
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

// serviceListOptions 构建 Service 资源的 List/Watch 选项。
// 按 metadata.name 精确匹配服务资源名，而非依赖 app= label 约定。
func (r *Registry) serviceListOptions(serviceName string) metav1.ListOptions {
	opts := metav1.ListOptions{
		LabelSelector: r.config.LabelSelector,
	}

	var fs []string
	if serviceName != "" {
		fs = append(fs, "metadata.name="+serviceName)
	}
	if r.config.ServiceType != "" && r.config.ServiceType != "All" {
		fs = append(fs, "spec.type="+r.config.ServiceType)
	}
	if len(fs) > 0 {
		opts.FieldSelector = strings.Join(fs, ",")
	}

	return opts
}

// endpointSliceSelector 构建 EndpointSlice 的 label selector。
// K8s 自动为 EndpointSlice 打 kubernetes.io/service-name 标签，直接用它匹配；
// 不叠加 Config.LabelSelector：非内置控制器管理的 EndpointSlice 未必复制 Service 的 label，
// label 过滤由 Service 侧完成，再按归属关联端点（见 buildServiceInfos）。
func endpointSliceSelector(serviceName string) string {
	if serviceName != "" {
		return discoveryv1.LabelServiceName + "=" + serviceName
	}
	return discoveryv1.LabelServiceName
}

// Close 关闭所有监听器
func (r *Registry) Close() error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	for id, cancel := range r.watchers {
		cancel()
		delete(r.watchers, id)
	}

	return nil
}
