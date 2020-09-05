package main

import (
	"log"

	"github.com/rushteam/beauty"
	"{{.ModPath}}router"
)

func main() {
	app := beauty.New()
	if err := app.Run(router.App()); err != nil {
		log.Fatalln(err)
	}
}
