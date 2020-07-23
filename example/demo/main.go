package main

import (
	"log"

	"github.com/rushteam/mojito"
	"github.com/rushteam/mojito/pkg/service/demo"
)

func main() {
	app := mojito.Init()
	s1 := demo.New()
	s2 := demo.New()
	err := app.Run(s1, s2)
	if err != nil {
		log.Fatalln(err)
	}
}
