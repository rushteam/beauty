package cmd

import (
	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli"
)

var Commands = []cli.Command{
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
