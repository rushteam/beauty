module github.com/rushteam/beauty/contrib/kafka

go 1.26.0

toolchain go1.26.5

require (
	github.com/rushteam/beauty v0.7.3
	github.com/twmb/franz-go v1.21.5
	github.com/twmb/franz-go/plugin/kotel v1.7.0
	go.opentelemetry.io/otel/trace v1.44.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.13.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
)

replace github.com/rushteam/beauty => ../../
