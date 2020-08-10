package main

import (
	"log"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/demo"
)

func main() {
	app := beauty.Init()
	s1 := demo.New()
	s2 := demo.New()
	err := app.Run(s1, s2)
	if err != nil {
		log.Fatalln(err)
	}
}
