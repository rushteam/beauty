package new

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gobuffalo/here"
	"github.com/markbates/pkger"
	"github.com/urfave/cli"
)

type project struct {
	Name    string
	Path    string
	ModPath string
}

//Project ..
var Project = &project{
	Name: "demo",
}

//Action ..
func Action(c *cli.Context) error {
	args := c.Args()
	if len(args) == 0 {
		fmt.Println("missing project name")
		return nil
	}
	if len(args[0]) > 0 {
		Project.Name = args[0]
	}
	//get abs path
	if Project.Path == "" {
		pwd, err := os.Getwd()
		if err == nil {
			return err
		}
		Project.Path = pwd
	} else {
		path, err := filepath.Abs(Project.Path)
		if err != nil {
			return err
		}
		Project.Path = path
	}

	//make project dir
	if err := MkdirAll(Project.Path, Project.Name); err != nil {
		return err
	}

	//get package path throuth mod env
	if hi, err := here.Current(); err == nil {
		if len(hi.ImportPath) > 0 {
			Project.ModPath = hi.ImportPath + "/"
		}
	}
	if err := pkger.Walk("/templates/web/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		src, err := pkger.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		data, err := ioutil.ReadAll(src)
		if err != nil {
			return err
		}
		tmpl, err := template.New(info.Name()).Parse(string(data))
		i := strings.Index(path, ":/templates/web/") + len(":/templates/web/")
		path = path[i:]
		path = strings.TrimSuffix(path, ".tpl")

		MkdirAll(Project.Path, Project.Name, filepath.Dir(path))

		dstPath := filepath.Join(Project.Path, Project.Name, path)
		dst, err := Create(dstPath)
		if err != nil {
			fmt.Println(err)
			return nil
		}
		defer dst.Close()
		tmpl.Execute(dst, Project)
		return nil
	}); err != nil {
		return err
	}
	return nil
}
