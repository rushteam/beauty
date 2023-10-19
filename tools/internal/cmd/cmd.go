package cmd

import (
	"fmt"

	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli/v2"
)

var Commands = []*cli.Command{
	{
		Name:   "new",
		Usage:  "new a project with template",
		Action: new.Action,
		BashComplete: func(*cli.Context) {
			fmt.Println("BashComplete??")
		},
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
