package log

import "go.uber.org/zap"

//DefaultLogger ..
var DefaultLogger *zap.Logger

func init() {
	DefaultLogger, _ = zap.NewDevelopment()
	// DefaultLogger, _ = zap.NewProduction()
}
