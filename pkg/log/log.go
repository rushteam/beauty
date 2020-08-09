package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

//DefaultLogger ..
var DefaultLogger *zap.Logger

//Logger ..
var Logger *zap.Logger

func init() {
	// DefaultLogger, _ = zap.NewProduction()
	DefaultLogger, _ = zap.NewDevelopment()
	Logger = DefaultLogger
}

//Info ..
func Info(msg string, fields ...zapcore.Field) {
	DefaultLogger.Info(msg, fields...)
}
