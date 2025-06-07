package cron

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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

func WitchTraceProvider(t trace.TracerProvider) CronOptions {
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
