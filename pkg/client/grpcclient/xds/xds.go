// Package xds 启用客户端 xDS 支持。
//
// 空导入本包即可注册 gRPC 内置的 xds:// name resolver 与 xDS 负载均衡策略：
//
//	import _ "github.com/rushteam/beauty/pkg/client/grpcclient/xds"
//
//	conn, _ := grpcclient.Dial("xds:///my-service", grpcclient.WithInsecure())
//
// 运行前需通过环境变量提供 xDS 引导配置（二选一）：
//
//	GRPC_XDS_BOOTSTRAP=/etc/grpc/xds_bootstrap.json   // 引导文件路径
//	GRPC_XDS_BOOTSTRAP_CONFIG='{...}'                 // 或直接内联 JSON
//
// 端点发现与负载均衡由控制平面（如 Istio/istiod）通过 LDS/RDS/CDS/EDS 下发，
// 因此 xDS 模式下 WithRegistry / WithLoadBalancer / 标签过滤等选项不生效。
package xds

import (
	"github.com/rushteam/beauty/pkg/client/grpcclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	xdscreds "google.golang.org/grpc/credentials/xds"

	_ "google.golang.org/grpc/xds" // 注册 xds:// resolver 与 xDS 负载均衡策略
)

// WithCredentials 返回使用 xDS 凭证的 DialOption：当控制平面下发 mTLS 配置时启用
// 双向 TLS，否则回退为明文。适用于安全 xDS 场景；纯明文场景直接用 grpcclient.WithInsecure()。
//
//	conn, _ := grpcclient.Dial("xds:///my-service", xds.WithCredentials())
func WithCredentials() grpcclient.DialOption {
	creds, err := xdscreds.NewClientCredentials(xdscreds.ClientOptions{
		FallbackCreds: insecure.NewCredentials(),
	})
	if err != nil {
		// 理论上仅校验选项，不会失败；保守回退到明文以保证可用。
		return grpcclient.WithInsecure()
	}
	return grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(creds))
}
