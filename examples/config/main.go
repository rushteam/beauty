// 配置热加载示例：beauty.WithConfig 接入文件/远程配置，变更自动重载。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/conf"
	// 远程配置需匿名导入对应 infra 包以注册 scheme，例如：
	// _ "github.com/rushteam/beauty/pkg/infra/etcd"
)

type AppConfig struct {
	Name     string `mapstructure:"name"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
}

func main() {
	// 本地文件：conf.New("config.yaml")；远程：conf.New("etcd://127.0.0.1:2379/app/config.yaml")
	//
	// WithStrictFields：配置里出现 AppConfig 没有的字段（如 key 拼写错误）时直接报错，
	// 不再静默忽略；去掉它即恢复宽松模式。
	loader, err := conf.New("config.yaml", conf.WithStrictFields())
	if err != nil {
		panic(err)
	}

	// WithSecrets 展开 ${...} 占位符，支持以下写法：
	//   ${env:NAME}            环境变量
	//   ${env:NAME:-8080}      环境变量 + 默认值
	//   ${PORT}                裸变量（等价 ${env:PORT}）
	//   ${PORT:-8080}          裸变量 + 默认值（shell 风格）
	//   ${file:/run/secret}    读取文件内容
	loader = conf.WithSecrets(loader)

	var current atomic.Pointer[AppConfig]

	mux := http.NewServeMux()
	mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
		if c := current.Load(); c != nil {
			slog.Info("serving config", "name", c.Name, "port", c.Port, "password", c.Password)
		}
		w.WriteHeader(http.StatusOK)
	})

	app := beauty.New(
		// 首次加载 + 变更时回调（热加载）
		beauty.WithConfig(loader, func(c *AppConfig) {
			current.Store(c)
			slog.Info("config (re)loaded", "name", c.Name, "port", c.Port, "password", c.Password)
		}),
		beauty.WithWebServer(":8080", mux),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
