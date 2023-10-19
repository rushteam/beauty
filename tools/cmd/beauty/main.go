package main

import (
	"log"
	"os"

	"github.com/rushteam/beauty/tools/internal/cmd"
	"github.com/urfave/cli"
)

// Version ..
var Version = "0.0.1"

func main() {
	app := cli.NewApp()
	app.Name = "beauty"
	app.Usage = "beauty tool"
	app.Version = Version
	app.Commands = cmd.Commands

	if err := app.Run(os.Args); err != nil {
		log.Fatalln("error:", err)
	}
}
