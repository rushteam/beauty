package main

import (
	"log"

	"gitlab.meitu.com/golang/beauty"
	"{{.ModPath}}router"
)

func main() {
	app := beauty.New()
	if err := app.Run(router.App()); err != nil {
		log.Fatalln(err)
	}
}
