package config

import "os"

//Dev ..
const Dev = "dev"

//Pre ..
const Pre = "pre"

//Prod ..
const Prod = "prod"

var env *environment

func init() {
	env = &environment{}
	env.Set(os.Getenv("ENVIRONMENT"))
}

type environment struct {
	Name string
}

func (e *environment) Set(name string) {
	if name == "" {
		name = Dev
	}
	e.Name = name
}

//Env get app env name
func Env() string {
	return env.Name
}
