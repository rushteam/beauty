package cron

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/store/dlock"
)

// specParser 与 cron.WithSeconds() 使用的解析器一致,用于在 Start 前提前校验 spec。
var specParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

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
		c.Name = getFunctionName(handler)
	}
	c.Handler = wrapCronHandler(cron, c.Name, c.Spec, handler)
	return c
}

type Cron struct {
	*cron.Cron
	// traceProvider/meterProvider 仅在 New() 初始化阶段使用，之后为 nil
	traceProvider trace.TracerProvider
	meterProvider metric.MeterProvider
	tracer        trace.Tracer
	meter         metric.Meter

	metricsJobSpentDuration metric.Float64Histogram

	// mu 保护 handlers / entries / runCtx:Start 前 Add/Remove 只改 handlers,
	// Start 后直接增删底层调度表并记录在 entries。
	mu       sync.Mutex
	handlers []cronHandler
	entries  map[string][]cron.EntryID
	runCtx   context.Context

	recoverHandler func(r any)

	// elector 非 nil 时,Start 只在选主当选期间运行任务(见 WithLeaderElector)。
	elector     dlock.Elector
	electionKey string
}

func New(opts ...CronOptions) *Cron {
	c := &Cron{
		Cron:     cron.New(cron.WithSeconds()),
		handlers: []cronHandler{},
		entries:  map[string][]cron.EntryID{},
	}
	for _, o := range opts {
		o(c)
	}
	if c.traceProvider == nil {
		c.traceProvider = otel.GetTracerProvider()
	}
	c.tracer = c.traceProvider.Tracer(ScopeName)
	c.traceProvider = nil // 初始化完成，释放引用

	if c.meterProvider == nil {
		c.meterProvider = otel.GetMeterProvider()
	}
	c.meter = c.meterProvider.Meter(ScopeName)
	c.meterProvider = nil // 初始化完成，释放引用

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

// register 把所有 handler 注册进底层 cron.Cron(仅登记调度表,不启动)。
// AddFunc 只是登记条目,真正开始触发要靠 Cron.Start()——所以无论是否配置
// 选主都可以在 Start 一开始就注册,选主只需要控制 Cron.Start()/Stop() 本身。
func (s *Cron) register(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runCtx = ctx
	for _, v := range s.handlers {
		logger.Info("register cron", slog.String("name", v.Name), slog.String("expr", v.Spec))
		id, err := s.addEntry(ctx, v)
		if err != nil {
			logger.Error("register cron failed", slog.String("name", v.Name), slog.String("expr", v.Spec), slog.Any("err", err))
			continue
		}
		s.entries[v.Name] = append(s.entries[v.Name], id)
	}
}

func (s *Cron) addEntry(ctx context.Context, v cronHandler) (cron.EntryID, error) {
	return s.Cron.AddFunc(v.Spec, func() {
		defer func() {
			if r := recover(); r != nil {
				if s.recoverHandler != nil {
					s.recoverHandler(r)
				} else {
					logger.Error("panic recovered", slog.Any("name", v.Name), slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
				}
			}
		}()
		if err := v.Handler(ctx); err != nil {
			logger.Error("cron handler failed", slog.Any("err", err))
		}
		logger.Debug("cron handler success", slog.String("date", time.Now().Format("20060102")))
	})
}

// Add 在运行期注册一个定时任务;已有同名任务时先移除再注册(替换语义)。
// Start 前调用效果等同 WithCronHandler(但会替换同名任务),Start 后立即进入调度表。
// spec 非法时返回错误。
//
// 任务名默认取 handler 的函数名,匿名闭包的自动名很容易重复,运行期增删请用 HandlerName 显式命名。
func (s *Cron) Add(spec string, handler func(ctx context.Context) error, opts ...CronHandlerOptions) error {
	if _, err := specParser.Parse(spec); err != nil {
		return fmt.Errorf("cron: invalid spec %q: %w", spec, err)
	}
	h := newCronHandler(s, spec, handler, opts...)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = slices.DeleteFunc(s.handlers, func(v cronHandler) bool { return v.Name == h.Name })
	s.handlers = append(s.handlers, h)
	if s.runCtx == nil {
		return nil
	}
	for _, id := range s.entries[h.Name] {
		s.Cron.Remove(id)
	}
	delete(s.entries, h.Name)
	id, err := s.addEntry(s.runCtx, h)
	if err != nil {
		return err
	}
	s.entries[h.Name] = []cron.EntryID{id}
	logger.Info("register cron", slog.String("name", h.Name), slog.String("expr", h.Spec))
	return nil
}

// Remove 按名字移除定时任务(含 WithCronHandler 注册的同名任务),返回是否存在。
// 正在执行中的那一次不会被打断。
func (s *Cron) Remove(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.handlers)
	s.handlers = slices.DeleteFunc(s.handlers, func(v cronHandler) bool { return v.Name == name })
	found := len(s.handlers) != n
	for _, id := range s.entries[name] {
		s.Cron.Remove(id)
		found = true
	}
	delete(s.entries, name)
	return found
}

// Names 返回当前登记的任务名(按注册顺序,重名只出现一次)。
func (s *Cron) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.handlers))
	for _, v := range s.handlers {
		if !slices.Contains(names, v.Name) {
			names = append(names, v.Name)
		}
	}
	return names
}

func (s *Cron) Start(ctx context.Context) error {
	s.register(ctx)

	if s.elector == nil {
		s.Cron.Start()
		<-ctx.Done()
		s.Cron.Stop()
		return nil
	}

	// 选主模式:只在当选期间运行,失去 leader 立即停止并重新参选。
	// elector.Run 仅在 outer ctx 取消时返回(此时是 ctx.Err()),按 beauty 惯例
	// "ctx 取消触发的优雅停止"算正常退出,不作为错误上报。
	err := s.elector.Run(ctx, s.electionKey, func(leaderCtx context.Context) {
		logger.Info("cron: elected as leader, starting jobs", slog.String("key", s.electionKey))
		s.Cron.Start()
		<-leaderCtx.Done()
		logger.Info("cron: lost leadership, stopping jobs", slog.String("key", s.electionKey))
		<-s.Cron.Stop().Done() // 等运行中的任务跑完再放开 leader 身份
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (s *Cron) String() string {
	return "cron"
}
