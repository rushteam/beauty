package api

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rushteam/beauty/tools/internal/entity"
	"github.com/urfave/cli/v2"
)

// Action ..
func Action(c *cli.Context) error {
	args := c.Args()
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("missing project name"), 1)
	}
	if n := args.Get(0); len(n) > 0 {
		entity.Config.Name = n
	}
	//get abs path
	if entity.Config.Path == "" {
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
	spec, err := os.ReadFile(filepath.Join(entity.Config.Path, "api.spec"))
	if err != nil {
		return cli.Exit(fmt.Errorf("%w", err), 1)
	}
	fmt.Println("string(spec)", string(spec))

	// content := string(spec)
	// stmts, err := parser.Parser(strings.NewReader(content), "")
	// if err != nil {
	// 	return cli.Exit(fmt.Errorf("%w", err), 1)
	// }
	// ast.Inspect(stmts, func(node ast.Node) bool {
	// 	// fmt.Printf("node: %+v \n", n)
	// 	switch n := node.(type) {
	// 	case *ast.Service:
	// 		// gen += fmt.Sprintf("service %v: %+v\n", n.Name, n)
	// 		// n.
	// 		log.Println("service", n.Name)
	// 		for _, v := range n.Rpcs {
	// 			log.Println("\t", v)
	// 			if len(v.Routes) > 0 {
	// 				for _, r := range v.Routes {
	// 					log.Println("\t\t", r.URI)
	// 				}
	// 			}

	// 		}

	// 	}
	// 	return true
	// })
	return nil
}
