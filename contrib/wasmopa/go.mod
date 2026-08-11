module github.com/rushteam/beauty/contrib/wasmopa

go 1.26.0

toolchain go1.26.5

require (
	github.com/rushteam/beauty v0.7.3
	github.com/tetratelabs/wazero v1.12.0
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.82.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// 本地联调。发布前去掉 replace。
replace github.com/rushteam/beauty => ../..
