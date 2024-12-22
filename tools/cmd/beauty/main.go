package main

import (
	"context"
	"log"
	"os"

	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli/v3"
)

// Version ..
var Version = "0.0.1"

func main() {
	cmd := &cli.Command{
		Name:    "beauty",
		Usage:   "code generator for beauty projects",
		Version: Version,
		Commands: []*cli.Command{
			new.Command(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatalln("error:", err)
	}

	// app := cli.NewApp()
	// app.Name = "beauty"
	// app.Usage = "code generator for beauty projects"
	// app.Version = Version
	// app.Commands = cmd.Commands

	// if err := app.Run(os.Args); err != nil {
	// 	log.Fatalln("error:", err)
	// }
}
