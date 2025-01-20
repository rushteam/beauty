package config

import "{{.ImportPath}}internal/infra/conf"

type Config struct {
	conf.Server `mapstructure:",squash"`
}
