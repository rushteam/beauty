package tpls

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:web all:grpc all:cron all:unified all:clean all:addons
var files embed.FS

// AddonRoot 获取附加组件(docker/k8s/ci)的模板子目录
func AddonRoot(name string) (fs.FS, error) {
	sub, err := fs.Sub(files, "addons/"+name)
	if err != nil {
		return nil, fmt.Errorf("加载附加组件模板 %q 失败: %w", name, err)
	}
	return sub, nil
}

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

// CleanRoot 获取整洁架构模板
func CleanRoot() fs.FS {
	f, _ := fs.Sub(files, "clean")
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
	case "clean":
		return CleanRoot()
	default: // web-service
		return Root()
	}
}
