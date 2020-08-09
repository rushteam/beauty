package main

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/mojito"
	"github.com/rushteam/mojito/pkg/service/web"
)

func main() {
	app := mojito.Init()
	err := app.Run(service())
	if err != nil {
		log.Fatalln(err)
	}
}
func service() mojito.Service {
	api, err := web.Build("api")
	if err != nil {
		log.Fatalln(err)
	}
	api.GET("/", func(c *gin.Context) {
		c.String(200, "hi mojito")
	})
	return api
}
