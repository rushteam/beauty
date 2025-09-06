package entity

import "github.com/gobuffalo/here"

type Project struct {
	Name       string
	Module     string
	Path       string
	ImportPath string
	Web        string
	Template   string
	WithDocker bool
	WithK8s    bool
	Info       here.Info
	// 服务类型选择
	EnableWeb  bool // 是否启用 HTTP 服务
	EnableGrpc bool // 是否启用 gRPC 服务
	EnableCron bool // 是否启用定时任务服务
}

// Project ..
var Config = &Project{
	// Name: "demo",
}
