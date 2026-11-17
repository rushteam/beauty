// 冻结示例:只跑已经下降好的脚本,不链接 mdrt,也不链接 llm。
//
//	cd contrib/mdrt
//	go run ./example/frozen
package main

import (
	"context"
	"embed"
	"log"
	"os"
	"os/signal"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/jsvm"
	"github.com/rushteam/beauty/pkg/messaging/eventbus"
)

//go:embed gen/*.js
var generated embed.FS

// hostConf 的字段名会按首字母小写暴露给脚本。
type hostConf struct {
	JwtSecret string
	NowMs     int64
}

func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "dev-secret"
	}
	vm, err := jsvm.New(
		jsvm.WithFS(generated, "gen/*.js"),
		jsvm.WithEventBus(eventbus.New[any]()),
		jsvm.WithGlobal("__mdrtConf", &hostConf{JwtSecret: secret}),
	)
	if err != nil {
		log.Fatal(err)
	}
	app := beauty.New(
		beauty.WithService(vm),
		beauty.WithWebServer(":8080", vm),
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
}
