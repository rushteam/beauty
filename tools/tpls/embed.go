package tpls

import (
	"embed"
	"io/fs"
)

//go:embed all:web all:grpc all:cron all:unified
var files embed.FS

// Root 获取web模板
func Root() fs.FS {
	f, _ := fs.Sub(files, "web")
	return f
}

// GrpcRoot 获取gRPC模板
func GrpcRoot() fs.FS {
	f, _ := fs.Sub(files, "grpc")
	return f
}

// CronRoot 获取定时任务模板
func CronRoot() fs.FS {
	f, _ := fs.Sub(files, "cron")
	return f
}

// UnifiedRoot 获取统一模板
func UnifiedRoot() fs.FS {
	f, _ := fs.Sub(files, "unified")
	return f
}

// GetTemplateRoot 根据模板类型获取模板根目录
func GetTemplateRoot(templateType string) fs.FS {
	switch templateType {
	case "grpc-service":
		return GrpcRoot()
	case "cron-service":
		return CronRoot()
	case "unified":
		return UnifiedRoot()
	default: // web-service
		return Root()
	}
}
