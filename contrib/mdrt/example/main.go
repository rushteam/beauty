// 示例:监听 specs/*.md,把「实现」段下降成 jsvm 脚本并热切换。不调用模型。
// 要让模型编译「行为」段,用 example/llm。
//
//	cd contrib/mdrt
//	go run ./example
//	curl -H 'Authorization: Bearer ...' -d '{"amount":1200}' localhost:8080/orders
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/mdrt"
)

func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "dev-secret"
	}
	rt, err := mdrt.New(
		mdrt.WithSpecDir("example/specs"),
		mdrt.WithStateDir(".mdrt"),
		mdrt.WithLockFile("mdrt.lock.json"),
		mdrt.WithMode(mdrt.ModeOffline),
		mdrt.WithJWTSecret(secret),
	)
	if err != nil {
		log.Fatal(err)
	}
	app := beauty.New(
		beauty.WithService(rt),
		beauty.WithWebServer(":8080", rt),
	)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
}
