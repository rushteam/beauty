package main

import (
	"log"

	"github.com/rushteam/mojito"
	"github.com/rushteam/mojito/pkg/service/demo"
)

func main() {
	app := mojito.Init()
	d1 := demo.New()
	d2 := demo.New()
	err := app.Run(d1, d2)
	if err != nil {
		log.Fatalln(err)
	}
}
