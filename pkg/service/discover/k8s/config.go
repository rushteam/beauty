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

// String 返回配置的字符串表示
func (c *Config) String() string {
	u := url.URL{
		Scheme: "k8s",
		Host:   c.Namespace,
	}

	if c.ServiceType != "" {
		u.Path = "/" + c.ServiceType
	}

	values := url.Values{}
	if c.Kubeconfig != "" {
		values.Set("kubeconfig", c.Kubeconfig)
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

// NewFromURL 从 URL 创建配置
func NewFromURL(u url.URL) (*Config, error) {
	c := &Config{}

	// 设置默认值
	c.Namespace = "default"
	c.ServiceType = "ClusterIP"
	c.WatchTimeout = 30

	// 从 URL 解析配置
	if u.Host != "" {
		c.Namespace = u.Host
	}

	if u.Path != "" {
		c.ServiceType = strings.TrimPrefix(u.Path, "/")
	}

	// 解析查询参数
	decoder := schema.NewDecoder()
	if err := decoder.Decode(c, u.Query()); err != nil {
		return nil, err
	}

	return c, nil
}
