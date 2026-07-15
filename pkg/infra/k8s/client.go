package k8s

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClientset 按 "给了 kubeconfig 路径就用文件、否则用集群内配置" 的惯例构造
// clientset。配置中心、选主与 pkg/service/discover/k8s 共用这一处构造,kubeconfig
// 为空且进程不在集群内运行时,rest.InClusterConfig 会返回错误。
func NewClientset(kubeconfig string) (kubernetes.Interface, error) {
	var (
		cfg *rest.Config
		err error
	)
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("k8s: build rest config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: new clientset: %w", err)
	}
	return client, nil
}
