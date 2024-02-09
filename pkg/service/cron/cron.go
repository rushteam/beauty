package cron

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

type cronHandler struct {
	Spec    string
	Handler func(ctx context.Context) error
}

type CronOptions func(c *Cron)

func WithCronHandler(spec string, handler func(ctx context.Context) error) CronOptions {
	return func(c *Cron) {
		c.handlers = append(c.handlers, cronHandler{
			Spec:    spec,
			Handler: handler,
		})
	}
}

type Cron struct {
	*cron.Cron
	handlers []cronHandler
}

func New(opts ...CronOptions) *Cron {
	c := &Cron{
		Cron:     cron.New(cron.WithSeconds()),
		handlers: []cronHandler{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (s *Cron) Start(ctx context.Context) error {
	for _, v := range s.handlers {
		func(v cronHandler) {
			slog.Info(fmt.Sprintf("register cron: %s", v.Spec))
			s.Cron.AddFunc(v.Spec, func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error(fmt.Sprintf("panic recovered: %v", r))
					}
				}()
				if err := v.Handler(ctx); err != nil {
					slog.Error("cron handler failed", slog.Any("handler", v.Handler))
				}
				slog.Debug("cron handler success", slog.String("date", time.Now().Format("20060102")))
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
