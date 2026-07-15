package k8s

import (
	"net/url"

	"github.com/rushteam/beauty/pkg/conf"
)

func init() {
	conf.RegisterFactory("configmap", newConfigMapCenterFromURL)
	conf.RegisterFactory("secret", newSecretCenterFromURL)
}

// newConfigMapCenterFromURL 从 URL 构造 ConfigMap 配置中心。
// 格式:configmap://<namespace>/<name>/<dataKey>?kubeconfig=/path/to/kubeconfig
// namespace 省略(configmap:///<name>/<dataKey>)时用 "default";kubeconfig 省略时
// 用集群内配置。上层 conf.New 会把 URL path("<name>/<dataKey>")作为 key 传给
// Get/Watch,并按其扩展名推断配置格式(可用 ?format=json 覆盖)。
//
// 例:configmap://prod/app-config/app.yaml
func newConfigMapCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	return newCenterFromURL(u, kindConfigMap)
}

// newSecretCenterFromURL 从 URL 构造 Secret 配置中心(格式同 configmap,scheme 为 secret)。
// 例:secret://prod/app-secret/config.yaml
func newSecretCenterFromURL(u *url.URL) (conf.ConfigCenter, error) {
	return newCenterFromURL(u, kindSecret)
}

func newCenterFromURL(u *url.URL, kind resourceKind) (conf.ConfigCenter, error) {
	client, err := NewClientset(u.Query().Get("kubeconfig"))
	if err != nil {
		return nil, err
	}
	return newConfigCenter(client, u.Host, kind), nil
}
