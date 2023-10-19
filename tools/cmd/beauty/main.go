package main

import (
	"log"
	"os"

	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli"
)

// Version ..
var Version = "0.0.1"

var commands = []cli.Command{
	{
		Name:   "new",
		Usage:  "new a project with template",
		Action: new.Action,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "d",
				Value:       "",
				Usage:       "specify the directory of the project",
				Destination: &new.Project.Path,
			},
		},
	},
}

func main() {
	app := cli.NewApp()
	app.Name = "beauty"
	app.Usage = "beauty tool"
	app.Version = Version
	app.Commands = commands

	if err := app.Run(os.Args); err != nil {
		log.Fatalln("error:", err)
	}
}
