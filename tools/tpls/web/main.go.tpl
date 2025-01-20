package main

import (
	"context"
	"flag"
	"log"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/endpoint/router"
	"{{.ImportPath}}internal/infra/conf"
	"{{.ImportPath}}internal/infra/logger"

	"github.com/rushteam/beauty"
)

var configPath string

func main() {
	flag.StringVar(&configPath, "config", "config/config.yaml", "")
	flag.Parse()

	var cfg = &config.Config{}
	if err := conf.Load(configPath, cfg); err != nil {
		log.Fatalln(err)
	}

	slog.SetDefault(logger.New(&cfg.Log))

	app := beauty.New(
		router.New(cfg),
	)

	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}
