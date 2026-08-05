package main

import (
	"fmt"

	_ "github.com/rushteam/beauty/pkg/service/discover/k8s" // 导入 k8s 服务发现
)

func main() {
	fmt.Println("=== K8s 服务发现示例 ===")

	fmt.Println("1. URL 格式（K8s DNS 风格）:")
	fmt.Println("   k8s://service.namespace?params")
	fmt.Println()

	fmt.Println("2. 服务发现配置示例:")
	fmt.Println("   精确服务:    k8s://my-service.default?port_name=grpc")
	fmt.Println("   指定namespace: k8s://payment-internal.mall?port_name=grpc&service_type=All")
	fmt.Println("   完整DNS格式:  k8s://my-svc.kube-system.svc.cluster.local?port_name=http")
	fmt.Println("   省略namespace: k8s://my-service?port_name=grpc              (默认 default)")
	fmt.Println("   集群外访问:   k8s://my-service.prod?kubeconfig=/path/to/config")
	fmt.Println("   通配+标签筛选: k8s://*.mall?label_selector=team=payment")
	fmt.Println()

	fmt.Println("3. gRPC 客户端使用:")
	fmt.Println(`   conn, err := grpc.Dial("k8s://my-service.default?port_name=grpc")`)
	fmt.Println()

	fmt.Println("4. 配置参数说明:")
	fmt.Println("   - namespace:      覆盖 Host 中的命名空间")
	fmt.Println("   - service_type:   服务类型过滤（默认 ClusterIP，设为 All 不过滤）")
	fmt.Println("   - port_name:      端口名称过滤（用于多端口服务）")
	fmt.Println("   - label_selector: K8s 标签选择器（例如 team=payment,version=v1）")
	fmt.Println("   - kubeconfig:     kubeconfig 文件路径（默认使用集群内配置）")
	fmt.Println("   - watch_timeout:  监听超时秒数（默认 30）")
	fmt.Println()

	fmt.Println("注意：实际使用需要在 Kubernetes 集群内运行或配置正确的 kubeconfig 文件")

	showIntegrationExample()
}

func showIntegrationExample() {
	fmt.Println("=== 集成示例代码 ===")
	fmt.Println()

	example := `
package main

import (
    "context"
    "log"

    "github.com/rushteam/beauty/pkg/service/discover"
    "github.com/rushteam/beauty/pkg/service/discover/k8s"
    "google.golang.org/grpc"

    _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/k8s" // 注册 gRPC resolver
)

func main() {
    // 方式一：通过 Config 直接构造
    config := &k8s.Config{
        Namespace:   "mall",
        ServiceType: "ClusterIP",
        PortName:    "grpc",
    }
    registry := k8s.NewRegistry(config)

    ctx := context.Background()
    services, err := registry.Find(ctx, "payment-internal")
    if err != nil {
        log.Fatal(err)
    }
    for _, svc := range services {
        log.Printf("发现服务: %s -> %s", svc.Name, svc.Addr)
    }

    // 监听服务变化
    registry.Watch(ctx, "payment-internal", func(services []discover.ServiceInfo) {
        log.Printf("服务变化，当前实例数: %d", len(services))
    })

    // 方式二：通过 gRPC resolver 直接 Dial（推荐）
    conn, err := grpc.Dial("k8s://payment-internal.mall?port_name=grpc",
        grpc.WithInsecure())
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
}
`

	fmt.Print(example)
}
