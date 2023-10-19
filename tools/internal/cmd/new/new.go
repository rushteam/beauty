package new

import (
	"fmt"
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
	"github.com/urfave/cli/v2"
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
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("missing project name"), 1)
	}
	if n := args.Get(0); len(n) > 0 {
		Project.Name = n
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
