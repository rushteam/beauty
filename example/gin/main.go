package main

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/rest"
)

func main() {
	app := beauty.New()
	err := app.Run(service())
	if err != nil {
		log.Fatalln(err)
	}
}
func service() beauty.Service {
	api, err := rest.Build("api")
	if err != nil {
		log.Fatalln(err)
	}
	api.GET("/", func(c *gin.Context) {
		c.String(200, "hi beauty")
	})
	return api
}
