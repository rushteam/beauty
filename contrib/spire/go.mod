module github.com/rushteam/beauty/contrib/spire

go 1.26.0

toolchain go1.26.5

require (
	github.com/rushteam/beauty v0.8.6
	github.com/spiffe/go-spiffe/v2 v2.6.0
	google.golang.org/grpc v1.82.1
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// 本地联调:依赖核心未发布的 resty.WithBaseTransport 等改动。
// 发布前请去掉 replace,并把 require 指向已发布的 beauty tag。
