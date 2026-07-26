package wasm

import (
	"context"
	"log/slog"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// 内置 host functions:开箱即用、但仍需**显式授予**(默认不注册)的常用能力,均挂在 "env" 模块下。
// 自定义能力用 WithHostFunc。

// WithLog 授予 guest 打日志的能力:注册 env.log(ptr i32, len i32),把 guest 内存 [ptr,ptr+len) 的
// UTF-8 文本经 logger 输出(nil 用 slog.Default())。
func WithLog(logger *slog.Logger) Option {
	if logger == nil {
		logger = slog.Default()
	}
	return WithHostFunc("env", "log", func(_ context.Context, m api.Module, ptr, n uint32) {
		if buf, ok := m.Memory().Read(ptr, n); ok {
			logger.Info("wasm.log", slog.String("msg", string(buf)))
		}
	})
}

// WithClock 授予 guest 读当前时间的能力(默认无 WASI 时钟):注册 env.now_unix_milli() -> i64,
// 返回自 Unix 纪元的毫秒数。
func WithClock() Option {
	return WithHostFunc("env", "now_unix_milli", func(_ context.Context, _ api.Module) int64 {
		return time.Now().UnixMilli()
	})
}
