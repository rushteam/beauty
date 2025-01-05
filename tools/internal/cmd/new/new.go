package new

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"
	"github.com/gobuffalo/here"
	"github.com/rushteam/beauty/tools/internal/entity"
	"github.com/rushteam/beauty/tools/internal/pkg"
	"github.com/rushteam/beauty/tools/tpls"
	"github.com/urfave/cli/v3"
)

func Command() *cli.Command {
	return &cli.Command{
		Name:   "new",
		Usage:  "new a project with template",
		Action: action,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "d",
				Value:       "",
				Usage:       "specify the directory of the project",
				Destination: &entity.Config.Path,
			},
		},
	}
}

func action(ctx context.Context, c *cli.Command) error {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Value(&entity.Config.Module).
				Title("Project Module Name").
				Validate(func(module string) error {
					if len(module) == 0 {
						return errors.New("enter a project model name")
					}
					name := filepath.Base(entity.Config.Module)
					// fmt.Println(name, module)
					if err := hasExists(name); err != nil {
						return err
					}
					entity.Config.Name = name
					return nil
				}),
			huh.NewSelect[string]().
				Title("Choose web fw").
				Options(
					huh.NewOption("chi", "chi"),
					huh.NewOption("gin", "gin"),
				).
				Value(&entity.Config.Web),
			huh.NewSelect[string]().
				Title("Choose web fw").
				Options(
					huh.NewOption("chi", "chi"),
					huh.NewOption("gin", "gin"),
				).
				Value(&entity.Config.Web),

			// huh.NewInput().
			// 	Value(&entity.Config.Module).
			// 	Title("Module Name"),
		),
	)
	if err := form.Run(); err != nil {
		fmt.Println(err)
		return nil
	}
	// entity.Config.Name = filepath.Base(entity.Config.Module)
	entity.Config.ImportPath = entity.Config.Module + "/"
	fmt.Println(entity.Config)
	buildProject(entity.Config)
	return nil
}

func hasExists(path string) error {
	dirs, err := os.ReadDir(".")
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if dir.Name() == path && dir.IsDir() {
			return errors.New("directory already exists")
		}
	}
	return nil
}

func buildProject(conf *entity.Project) error {
	if err := hasExists(conf.Name); err != nil {
		return err
	}
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	conf.Path = filepath.Join(pwd, conf.Name)
	tpl := tpls.Root()
	return fs.WalkDir(tpl, ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			pkg.MkdirAll(filepath.Join(conf.Path, path))
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
		outputPath := filepath.Join(conf.Path, filename)
		log.Println("create file:", outputPath)

		tmpl, err := template.New(info.Name()).Parse(string(data))
		if err != nil {
			return err
		}
		fmt.Println(outputPath)
		dst, err := pkg.Create(outputPath)
		if err != nil {
			return nil
		}
		defer dst.Close()
		return tmpl.Execute(dst, entity.Config)
	})
}

func action2(ctx context.Context, c *cli.Command) error {
	args := c.Args()
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("missing project name"), 1)
	}
	if v := args.Get(0); len(v) > 0 {
		entity.Config.Name = v
	}
	//get abs path
	if len(entity.Config.Path) == 0 {
		pwd, err := os.Getwd()
		if err != nil {
			return err
		}
		entity.Config.Path = filepath.Join(pwd, entity.Config.Name)
	} else {
		path, err := filepath.Abs(entity.Config.Path)
		if err != nil {
			return err
		}
		entity.Config.Path = path
	}
	//make project dir
	if err := pkg.MkdirAll(entity.Config.Path); err != nil {
		return err
	}
	//get package path throuth mod env
	// if hi, err := here.Current(); err == nil {
	if hi, err := here.Dir(entity.Config.Path); err == nil {
		entity.Config.Info = hi
		if len(hi.ImportPath) > 0 {
			entity.Config.Module = hi.ImportPath
		}
		entity.Config.ImportPath = entity.Config.Module + "/"
	}
	log.Println("create project:", entity.Config.Name)
	log.Println("path:", entity.Config.Path)
	log.Println("module:", entity.Config.Module)

	tpl := tpls.Root()
	err := fs.WalkDir(tpl, ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			pkg.MkdirAll(filepath.Join(entity.Config.Path, path))
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
		outputPath := filepath.Join(entity.Config.Path, filename)
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
		return tmpl.Execute(dst, entity.Config)
	})
	if err != nil {
		return err
	}
	return nil
}
