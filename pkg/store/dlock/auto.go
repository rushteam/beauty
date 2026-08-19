package dlock

import (
	"fmt"
	"net/url"
	"os"
)

// NewElectorAuto 根据运行环境自动选择 Elector 后端：
//
//   - 检测到 KUBERNETES_SERVICE_HOST → 使用 k8s Lease 选主
//     （需提前 import _ "github.com/rushteam/beauty/pkg/infra/k8s"）
//   - 否则 → 使用内存选主（单实例/本地开发）
//
// Lease 资源名由调用方在 Elector.Run(ctx, key, ...) 时通过 key 参数指定，
// 不在此处配置。可通过 DLOCK_ELECTOR_URL 环境变量强制指定后端 DSN，覆盖自动检测。
func NewElectorAuto() (Elector, error) {
	if dsn := os.Getenv("DLOCK_ELECTOR_URL"); dsn != "" {
		return NewElector(dsn)
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return newK8sElector()
	}
	return NewMemory(), nil
}

func newK8sElector() (Elector, error) {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		ns = "default"
	}
	u := &url.URL{
		Scheme:   "k8s",
		RawQuery: url.Values{"namespace": {ns}}.Encode(),
	}

	factoryMu.RLock()
	fn, ok := electorFactories["k8s"]
	factoryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("dlock: k8s elector factory not registered — import _ \"github.com/rushteam/beauty/pkg/infra/k8s\"")
	}
	return fn(u)
}
