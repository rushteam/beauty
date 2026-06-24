// xDS 客户端示例：通过 xds:///service 目标，由 xDS 控制平面（如 Istio/istiod）
// 下发端点与负载均衡策略。
//
// 运行前提供引导配置（二选一）：
//
//	export GRPC_XDS_BOOTSTRAP=$(pwd)/xds_bootstrap.example.json
//	# 或
//	export GRPC_XDS_BOOTSTRAP_CONFIG="$(cat xds_bootstrap.example.json)"
//
// 然后：go run .
package main

import (
	"context"
	"log"
	"time"

	"github.com/rushteam/beauty/pkg/client/grpcclient"

	// 空导入以注册 gRPC 内置的 xds:// resolver 与 xDS 负载均衡策略。
	_ "github.com/rushteam/beauty/pkg/client/grpcclient/xds"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 明文 xDS：
	conn, err := grpcclient.DialContext(ctx, "xds:///my-service", grpcclient.WithInsecure())
	// 安全 xDS（控制平面下发 mTLS，回退明文），改用：
	//   import beautyxds "github.com/rushteam/beauty/pkg/client/grpcclient/xds"
	//   conn, err := grpcclient.DialContext(ctx, "xds:///my-service", beautyxds.WithCredentials())
	if err != nil {
		log.Fatalf("dial xds:///my-service 失败: %v", err)
	}
	defer conn.Close()

	log.Printf("✅ 已通过 xDS 建链, 当前连接状态=%s", conn.GetState())
	// 接下来用 conn 构造你的 gRPC stub 并发起调用，例如：
	//   client := pb.NewGreeterClient(conn)
	//   resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: "beauty"})
}
