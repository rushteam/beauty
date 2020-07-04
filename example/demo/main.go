package main

import (
	"log"

	"github.com/rushteam/mojito"
	"github.com/rushteam/mojito/service/demo"
)

func main() {
	app := mojito.Init()
	d := demo.New()
	err := app.Run(d)
	if err != nil {
		log.Fatalln(err)
	}
}
