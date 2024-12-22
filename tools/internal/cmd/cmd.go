package cmd

import (
	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli/v3"
)

var Commands = []*cli.Command{
	new.Command(),

	// {
	// 	Name:   "api",
	// 	Usage:  "gen rpc/api with api specify (.api)",
	// 	Action: api.Action,
	// 	BashComplete: func(*cli.Context) {
	// 		fmt.Println("BashComplete??")
	// 	},
	// 	Flags: []cli.Flag{
	// 		&cli.StringFlag{
	// 			Name:        "d",
	// 			Value:       "",
	// 			Usage:       "specify the directory of the project",
	// 			Destination: &project.Config.Path,
	// 		},
	// 	},
	// },
}
