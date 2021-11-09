package main

import (
	"flag"
	"log"
	"runtime"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/web"
)

var confFile = flag.String("config", "./config.yaml", "config path")

func main() {
	flag.Parse()
	// var c config.Config
	// conf.LoadConfig(*confFile, &c)
	app := beauty.New(
	// beauty.WithServer(web.New(
	// 	"api",
	// 	web.WithAddr(":8080"),
	// 	web.WithRouter(router),
	// )),
	)
	err := app.Run(
		web.New(
			"api",
			web.WithAddr(":8080"),
			router,
		),
	)
	if err != nil {
		log.Fatal(err)
	}
}

func router(r *web.WebServer) {
	r.GET("/", func(c *gin.Context) {
		c.String(200, "hi beauty")
	})
	r.GET("/version", func(c *gin.Context) {
		c.String(200, runtime.Version())
	})
}
