package telemetry

import (
	"sync"

	"go.opentelemetry.io/otel"

	"github.com/rushteam/beauty/pkg/service/logger"
)

var errorHandlerOnce sync.Once

// setupErrorHandler 将 OTel 内部错误接入项目 logger。
//
// OTel 的导出（trace/metric 上报 Collector）是异步的：endpoint 写错、Collector 挂掉、
// 批处理队列溢出等运行时错误不会 panic、也不会从业务调用栈返回，而是交给全局 ErrorHandler。
// 默认 handler 直接打到 stderr、绕过结构化日志，等于「静默」。这里统一接到 logger.Error。
//
// 由 tracer/metric 组件的 Init 调用，sync.Once 保证只设置一次。
func setupErrorHandler() {
	errorHandlerOnce.Do(func() {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			logger.Error("otel error", "error", err)
		}))
	})
}
