package new

import (
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gobuffalo/here"
	"github.com/rushteam/beauty/tools/internal/pkg"
	"github.com/rushteam/beauty/tools/tpls"
	"github.com/urfave/cli"
)

type project struct {
	Name    string
	Path    string
	ModPath string
}

// Project ..
var Project = &project{
	Name: "demo",
}

// Action ..
func Action(c *cli.Context) error {
	args := c.Args()
	if len(args) == 0 {
		log.Println("missing project name")
		return nil
	}
	if len(args[0]) > 0 {
		Project.Name = args[0]
	}
	//get abs path
	if Project.Path == "" {
		pwd, err := os.Getwd()
		if err != nil {
			return err
		}
		Project.Path = filepath.Join(pwd, Project.Name)
	} else {
		path, err := filepath.Abs(Project.Path)
		if err != nil {
			return err
		}
		Project.Path = path
	}

	//make project dir
	if err := pkg.MkdirAll(Project.Path); err != nil {
		return err
	}
	log.Println("make project dir:", Project.Path)

	//get package path throuth mod env

	// if hi, err := here.Current(); err == nil {
	if hi, err := here.Dir(Project.Path); err == nil {
		log.Println("???", hi)
		if len(hi.ImportPath) > 0 {
			Project.ModPath = hi.ImportPath + "/"
		}
	}
	tpl := tpls.Root()
	fs.WalkDir(tpl, ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			pkg.MkdirAll(filepath.Join(Project.Path, path))
			return nil
		}
		src, err := tpl.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		data, err := io.ReadAll(src)
		if err != nil {
			return err
		}
		filename := strings.TrimSuffix(path, ".tpl")
		outputPath := filepath.Join(Project.Path, filename)
		log.Println("create file:", outputPath)
		tmpl, err := template.New(info.Name()).Parse(string(data))
		if err != nil {
			return err
		}
		dst, err := pkg.Create(outputPath)
		if err != nil {
			return nil
		}
		defer dst.Close()
		log.Println("Project.ModPath", Project)
		return tmpl.Execute(dst, Project)
	})
	return nil
}
