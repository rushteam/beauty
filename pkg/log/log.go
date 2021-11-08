package log

import (
	"go.uber.org/zap"
)

//Logger ..
// var Logger, _ = zap.NewProduction()
var Logger, _ = zap.NewDevelopment()

//Debug ..
func Debug(msg string, fields ...zap.Field) {
	Logger.Debug(msg, fields...)
}

//Info ..
func Info(msg string, fields ...zap.Field) {
	Logger.Info(msg, fields...)
}

//Warn ..
func Warn(msg string, fields ...zap.Field) {
	Logger.Warn(msg, fields...)
}

//Error ..
func Error(msg string, fields ...zap.Field) {
	Logger.Warn(msg, fields...)
}

//Panic ..
func Panic(msg string, fields ...zap.Field) {
	Logger.Panic(msg, fields...)
}

//Fatal ..
func Fatal(msg string, fields ...zap.Field) {
	Logger.Fatal(msg, fields...)
}
