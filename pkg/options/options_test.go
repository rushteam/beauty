package options_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/options"
)

// —— 模拟框架侧通用选项 ——
type commonOpts struct {
	Timeout time.Duration
	Name    string
}

func WithTimeout(d time.Duration) options.Option {
	return options.Common(func(o *commonOpts) { o.Timeout = d })
}
func WithName(n string) options.Option {
	return options.Common(func(o *commonOpts) { o.Name = n })
}

// —— 模拟实现方 A 的专属选项 ——
type redisOpts struct{ PoolSize int }

func WithPoolSize(n int) options.Option {
	return options.Impl(func(o *redisOpts) { o.PoolSize = n })
}

// —— 模拟实现方 B 的专属选项（与 A 不同类型）——
type kafkaOpts struct{ Partitions int }

func WithPartitions(n int) options.Option {
	return options.Impl(func(o *kafkaOpts) { o.Partitions = n })
}

func TestLayeredOptions(t *testing.T) {
	opts := []options.Option{
		WithTimeout(3 * time.Second),
		WithName("svc"),
		WithPoolSize(50),
		WithPartitions(8), // 属于另一实现，redis 提取时应被忽略
	}

	common := options.ApplyCommon(&commonOpts{Timeout: time.Second}, opts...)
	if common.Timeout != 3*time.Second || common.Name != "svc" {
		t.Fatalf("common opts not applied: %+v", common)
	}

	redis := options.ApplyImpl(&redisOpts{PoolSize: 10}, opts...)
	if redis.PoolSize != 50 {
		t.Fatalf("redis opts not applied: %+v", redis)
	}

	// 实现 B 只应拿到自己的专属选项，redis 的不串台
	kafka := options.ApplyImpl(&kafkaOpts{}, opts...)
	if kafka.Partitions != 8 {
		t.Fatalf("kafka opts not applied: %+v", kafka)
	}
}

func TestDefaultsPreservedWhenNoOption(t *testing.T) {
	common := options.ApplyCommon(&commonOpts{Timeout: time.Second})
	if common.Timeout != time.Second {
		t.Fatalf("default should be preserved, got %v", common.Timeout)
	}
}
