package entity

import "github.com/gobuffalo/here"

type Project struct {
	Name       string
	Module     string
	Path       string
	ImportPath string
	Web        string
	Info       here.Info
}

// Project ..
var Config = &Project{
	// Name: "demo",
}
