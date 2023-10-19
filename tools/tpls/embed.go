package tpls

import (
	"embed"
	"io/fs"
)

//go:embed all:web
var files embed.FS

func Root() fs.FS {
	f, _ := fs.Sub(files, "web")
	return f
}
