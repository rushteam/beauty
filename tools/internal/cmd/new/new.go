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
	"github.com/rushteam/beauty/tools/internal/project"
	"github.com/rushteam/beauty/tools/tpls"
	"github.com/urfave/cli/v2"
)

// Action ..
func Action(c *cli.Context) error {
	args := c.Args()
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("missing project name"), 1)
	}
	if n := args.Get(0); len(n) > 0 {
		project.Config.Name = n
	}
	//get abs path
	if project.Config.Path == "" {
		pwd, err := os.Getwd()
		if err != nil {
			return err
		}
		project.Config.Path = filepath.Join(pwd, project.Config.Name)
	} else {
		path, err := filepath.Abs(project.Config.Path)
		if err != nil {
			return err
		}
		project.Config.Path = path
	}
	//make project dir
	if err := pkg.MkdirAll(project.Config.Path); err != nil {
		return err
	}
	//get package path throuth mod env
	// if hi, err := here.Current(); err == nil {
	if hi, err := here.Dir(project.Config.Path); err == nil {
		if len(hi.ImportPath) > 0 {
			project.Config.ModPath = hi.ImportPath + "/"
		}
	}
	log.Println("make project dir:", project.Config.Path)
	tpl := tpls.Root()
	fs.WalkDir(tpl, ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			pkg.MkdirAll(filepath.Join(project.Config.Path, path))
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
		outputPath := filepath.Join(project.Config.Path, filename)
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
		// log.Println("Project.ModPath", Project)
		return tmpl.Execute(dst, project.Config)
	})
	return nil
}
