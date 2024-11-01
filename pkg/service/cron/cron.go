package cron

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/logger"
)

type cronHandler struct {
	Name    string
	Spec    string
	Handler func(ctx context.Context) error
}

func newCronHandler(cron *Cron, spec string, handler func(ctx context.Context) error, opts ...CronHandlerOptions) cronHandler {
	var cfg cronHandlerCfg
	for _, o := range opts {
		o(&cfg)
	}
	c := cronHandler{
		Name: cfg.name,
		Spec: spec,
	}
	if c.Name == "" {
		c.Name = getCallerShortInfo(3) // 因为没有其他更合适的名字，所以先取调用者的文件名和行号
	}
	c.Handler = wrapCronHandler(cron, c.Name, c.Spec, handler)
	return c
}

type Cron struct {
	*cron.Cron
	traceProvider trace.TracerProvider
	tracer        trace.Tracer

	meterProvider metric.MeterProvider
	meter         metric.Meter

	metricsJobSpentDuration metric.Float64Histogram
	handlers                []cronHandler
}

func New(opts ...CronOptions) *Cron {
	c := &Cron{
		Cron:     cron.New(cron.WithSeconds()),
		handlers: []cronHandler{},
	}
	for _, o := range opts {
		o(c)
	}
	if c.traceProvider == nil {
		c.traceProvider = otel.GetTracerProvider()
	}
	c.tracer = c.traceProvider.Tracer(ScopeName)

	if c.meterProvider == nil {
		c.meterProvider = otel.GetMeterProvider()
	}
	c.meter = c.meterProvider.Meter(ScopeName)

	var err error
	c.metricsJobSpentDuration, err = c.meter.Float64Histogram(
		"beauty.cron.job.spent.duration",
		metric.WithExplicitBucketBoundaries(
			0, 0.01, 0.1, 0.5, 1, 5, 10, 30, 60, 180, 300, 600, 1800),
		metric.WithDescription("The cron job spent time duration (s)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		otel.Handle(err)
		if c.metricsJobSpentDuration == nil {
			c.metricsJobSpentDuration = noop.Float64Histogram{}
		}
	}

	return c
}

func (s *Cron) Start(ctx context.Context) error {
	for _, v := range s.handlers {
		func(v cronHandler) {
			logger.Info("register cron", slog.String("expr", v.Spec))
			s.Cron.AddFunc(v.Spec, func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("panic recovered", slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
					}
				}()
				if err := v.Handler(ctx); err != nil {
					logger.Error("cron handler failed", slog.Any("err", err))
				}
				logger.Debug("cron handler success", slog.String("date", time.Now().Format("20060102")))
			})
		}(v)
	}
	s.Cron.Start()
	<-ctx.Done()
	s.Cron.Stop()
	return nil
}

func (s *Cron) String() string {
	return "cron"
}
