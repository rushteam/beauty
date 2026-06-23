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
	Name string `mapstructure:"name"`
	Port int    `mapstructure:"port"`
}

func main() {
	// 本地文件：conf.New("config.yaml")；远程：conf.New("etcd://127.0.0.1:2379/app/config.yaml")
	loader, err := conf.New("config.yaml")
	if err != nil {
		panic(err)
	}

	var current atomic.Pointer[AppConfig]

	mux := http.NewServeMux()
	mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
		if c := current.Load(); c != nil {
			slog.Info("serving config", "name", c.Name, "port", c.Port)
		}
		w.WriteHeader(http.StatusOK)
	})

	app := beauty.New(
		// 首次加载 + 变更时回调（热加载）
		beauty.WithConfig(loader, func(c *AppConfig) {
			current.Store(c)
			slog.Info("config (re)loaded", "name", c.Name, "port", c.Port)
		}),
		beauty.WithWebServer(":8080", mux),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
