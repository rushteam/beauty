package project

type Project struct {
	Name    string
	Path    string
	ModPath string
}

// Project ..
var Config = &Project{
	Name: "demo",
}
