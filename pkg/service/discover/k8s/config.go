package k8s

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/schema"
)

// Config k8s 服务发现配置
type Config struct {
	// Kubeconfig 文件路径，为空时使用集群内配置
	Kubeconfig string `mapstructure:"kubeconfig" schema:"kubeconfig"`
	// Namespace 命名空间，默认为 default
	Namespace string `mapstructure:"namespace" schema:"namespace"`
	// ServiceType 服务类型，默认为 ClusterIP
	ServiceType string `mapstructure:"service_type" schema:"service_type"`
	// PortName 端口名称，用于多端口服务
	PortName string `mapstructure:"port_name" schema:"port_name"`
	// LabelSelector 标签选择器，用于过滤服务
	LabelSelector string `mapstructure:"label_selector" schema:"label_selector"`
	// WatchTimeout 监听超时时间（秒），默认30秒
	WatchTimeout int `mapstructure:"watch_timeout" schema:"watch_timeout"`
}

// String 返回配置的字符串表示（用作 registry 单例缓存 key）。
func (c *Config) String() string {
	u := url.URL{
		Scheme: "k8s",
		Host:   c.Namespace,
	}

	values := url.Values{}
	if c.Kubeconfig != "" {
		values.Set("kubeconfig", c.Kubeconfig)
	}
	if c.ServiceType != "" && c.ServiceType != "ClusterIP" {
		values.Set("service_type", c.ServiceType)
	}
	if c.PortName != "" {
		values.Set("port_name", c.PortName)
	}
	if c.LabelSelector != "" {
		values.Set("label_selector", c.LabelSelector)
	}
	if c.WatchTimeout > 0 {
		values.Set("watch_timeout", strconv.Itoa(c.WatchTimeout))
	}

	u.RawQuery = values.Encode()
	return u.String()
}

// NewFromURL 从 URL 创建配置。格式遵循 K8s DNS 风格：
//
//	k8s://service.namespace[.svc[.cluster.local]]?params
//
// 示例：
//
//	k8s://payment-internal.mall?port_name=grpc
//	k8s://my-svc.kube-system.svc.cluster.local?port_name=http
//	k8s://payment-internal?port_name=grpc                        (省略 namespace → default)
//
// Host 中第一段是服务名（由 grpcclient / gRPC resolver 消费），第二段是 namespace。
// 无 "." 时 namespace 为 "default"，与 K8s DNS 语义一致。
// service_type 仅通过 query 参数 ?service_type=... 指定，默认 ClusterIP。
func NewFromURL(u url.URL) (*Config, error) {
	c := &Config{}

	c.Namespace = "default"
	c.ServiceType = "ClusterIP"
	c.WatchTimeout = 30

	if u.Host != "" {
		if i := strings.IndexByte(u.Host, '.'); i >= 0 {
			// service.namespace[.svc[.cluster.local]] → 取第二段为 namespace
			rest := u.Host[i+1:]
			if j := strings.IndexByte(rest, '.'); j >= 0 {
				c.Namespace = rest[:j]
			} else {
				c.Namespace = rest
			}
		}
		// 无 "." → namespace 保持 "default"（K8s DNS：裸名 = default 命名空间）
	}

	decoder := schema.NewDecoder()
	if err := decoder.Decode(c, u.Query()); err != nil {
		return nil, err
	}

	return c, nil
}
