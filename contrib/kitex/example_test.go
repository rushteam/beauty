package kitex_test

import (
	"fmt"

	beautyKitex "github.com/rushteam/beauty/contrib/kitex"
)

func ExampleNew() {
	srv := beautyKitex.New(":8888",
		beautyKitex.WithServiceName("example.shop.item"),
		beautyKitex.WithWeight(100),
	)

	// 使用 srv.Server() 获取底层 kitex server.Server 注册 handler：
	// itemservice.RegisterService(srv.Server(), new(ItemServiceImpl))

	// 传入 beauty.WithService(srv) 即可
	fmt.Println("server name:", srv.Name())
	fmt.Println("server kind:", srv.Kind())
	// Output:
	// server name: example.shop.item
	// server kind: thrift
}

func ExampleNewResolverAdapter() {
	// beauty.Discovery → kitex.Resolver 适配器
	// 假设 beautyDiscovery 实现了 discover.Discovery 接口
	//
	// adapter := beautyKitex.NewResolverAdapter(beautyDiscovery)
	// client := itemservice.NewClient("example.shop.item",
	//     client.WithResolver(adapter),
	// )

	adapter := beautyKitex.NewResolverAdapter(nil)
	fmt.Println("resolver name:", adapter.Name())
	// Output:
	// resolver name: beauty-discovery
}
