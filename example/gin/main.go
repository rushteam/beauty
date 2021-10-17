package main

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/web"
)

func main() {
	app := beauty.New()
	err := app.Run(webserver())
	if err != nil {
		log.Fatalln(err)
	}
}
func webserver() beauty.Service {
	api, err := web.New("api")
	if err != nil {
		log.Fatalln(err)
	}
	api.GET("/", func(c *gin.Context) {
		c.String(200, "hi beauty")
	})
	return api
}
