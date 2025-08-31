package main

import (
	"fmt"

	_ "github.com/rushteam/beauty/pkg/service/discover/k8s" // 导入 k8s 服务发现
)

func main() {
	// 示例1: 通过 URL 创建 k8s 服务发现客户端
	// k8s://default?kubeconfig=/path/to/kubeconfig&service_type=ClusterIP&port_name=http

	// 在集群内运行时的简单配置
	fmt.Println("=== K8s 服务发现示例 ===")

	// 这里演示如何在代码中使用，实际使用时通常通过配置文件或环境变量
	fmt.Println("1. 服务发现配置示例:")
	fmt.Println("   集群内: k8s://default")
	fmt.Println("   指定配置: k8s://my-namespace?kubeconfig=/path/to/config&service_type=ClusterIP")
	fmt.Println("   带标签选择器: k8s://default?label_selector=app=my-service,version=v1")
	fmt.Println()

	fmt.Println("2. gRPC 客户端使用示例:")
	fmt.Println(`   import "google.golang.org/grpc"`)
	fmt.Println(`   conn, err := grpc.Dial("k8s://default/my-service", grpc.WithInsecure())`)
	fmt.Println()

	fmt.Println("3. 服务发现功能:")
	fmt.Println("   - 自动发现 Kubernetes Service 和 Endpoints")
	fmt.Println("   - 支持服务变更监听")
	fmt.Println("   - 支持多端口服务")
	fmt.Println("   - 支持标签选择器过滤")
	fmt.Println("   - 支持命名空间隔离")
	fmt.Println()

	fmt.Println("4. 配置参数说明:")
	fmt.Println("   - kubeconfig: kubeconfig 文件路径（可选，默认使用集群内配置）")
	fmt.Println("   - namespace: 命名空间（默认 default）")
	fmt.Println("   - service_type: 服务类型过滤（默认 ClusterIP）")
	fmt.Println("   - port_name: 端口名称过滤（用于多端口服务）")
	fmt.Println("   - label_selector: 标签选择器（例如：app=my-service,version=v1）")
	fmt.Println("   - watch_timeout: 监听超时时间秒数（默认 30）")
	fmt.Println()

	// 注意：以下代码需要在 k8s 集群内或配置了正确的 kubeconfig 才能运行
	fmt.Println("注意：实际使用需要在 Kubernetes 集群内运行或配置正确的 kubeconfig 文件")

	// 演示如何在应用中集成
	showIntegrationExample()
}

func showIntegrationExample() {
	fmt.Println("=== 集成示例代码 ===")
	fmt.Println()

	example := `
// 在你的应用中集成 k8s 服务发现

package main

import (
    "context"
    "log"
    "time"
    
    "github.com/rushteam/beauty/pkg/service/discover/k8s"
    "google.golang.org/grpc"
)

func main() {
    // 1. 创建 k8s 服务发现配置
    config := &k8s.Config{
        Namespace:     "default",
        ServiceType:   "ClusterIP", 
        LabelSelector: "app=my-service",
        WatchTimeout:  30,
    }
    
    // 2. 创建注册中心
    registry := k8s.NewRegistry(config)
    
    // 3. 查找服务实例
    ctx := context.Background()
    services, err := registry.Find(ctx, "my-service")
    if err != nil {
        log.Fatal(err)
    }
    
    for _, svc := range services {
        log.Printf("发现服务: %s -> %s", svc.Name, svc.Addr)
    }
    
    // 4. 监听服务变化
    registry.Watch(ctx, "my-service", func(services []discover.ServiceInfo) {
        log.Printf("服务变化，当前实例数: %d", len(services))
        for _, svc := range services {
            log.Printf("  - %s: %s", svc.Name, svc.Addr)
        }
    })
    
    // 5. 或者直接在 gRPC 客户端中使用
    conn, err := grpc.Dial("k8s://default/my-service", 
        grpc.WithInsecure(),
        grpc.WithBlock(),
        grpc.WithTimeout(time.Second*5))
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    // 现在可以使用连接调用服务了
    // client := pb.NewMyServiceClient(conn)
    // resp, err := client.MyMethod(ctx, &pb.MyRequest{})
}
`

	fmt.Print(example)
}
