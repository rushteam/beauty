package cron

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/dlock"
)

type cronHandlerCfg struct {
	name string
}
type CronHandlerOptions func(cfg *cronHandlerCfg)

func HandlerName(name string) CronHandlerOptions {
	return func(cfg *cronHandlerCfg) {
		cfg.name = name
	}
}

func WithCronHandler(spec string, handler func(ctx context.Context) error, opts ...CronHandlerOptions) CronOptions {
	return func(c *Cron) {
		c.handlers = append(c.handlers, newCronHandler(c, spec, handler, opts...))
	}
}

type CronOptions func(c *Cron)

func WithTraceProvider(t trace.TracerProvider) CronOptions {
	return func(c *Cron) {
		c.traceProvider = t
	}
}

func WithMeterProvider(m metric.MeterProvider) CronOptions {
	return func(c *Cron) {
		c.meterProvider = m
	}
}

func WithRecover(recoverHandler func(r any)) CronOptions {
	return func(c *Cron) {
		c.recoverHandler = recoverHandler
	}
}

// WithLeaderElector 让整个 Cron 只在选主(leader election)当选期间跑任务,
// 解决"多实例部署下每个实例都各跑一遍定时任务"的重复执行问题。
//
// key 是参选的选举标识(同一组要互斥的多个实例须用同一个 key,通常按服务名
// 定,如 "myservice-cron")。elector 通常传 pkg/infra/etcd 的 DLock(生产,
// 跨实例)或 pkg/dlock.NewMemory()(单实例/测试)。
//
// 语义:未当选时不注册/不运行任何任务(Start 阻塞在选举上);当选后启动全部
// 任务,失去 leader 身份(网络分区/进程崩溃/主动放弃)时立即停止全部任务并
// 重新参选。不配置此项时行为不变(直接 Start,不经过选举)。
func WithLeaderElector(elector dlock.Elector, key string) CronOptions {
	return func(c *Cron) {
		c.elector = elector
		c.electionKey = key
	}
}
