package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rushteam/beauty"
)

func main() {
	s := &srv{}
	s2 := &srv{}
	app := beauty.New(beauty.WithService(s, s2), beauty.WithWebServer(":8080"))
	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}

type srv struct {
}

func (s *srv) Start(ctx context.Context) error {
	fmt.Println("..")
	return nil
}
func (s *srv) String() string {
	return "server"
}
