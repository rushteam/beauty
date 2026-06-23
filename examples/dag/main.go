// DAG 示例：build -> [test, lint 并行] -> deploy。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/pkg/dag"
)

func step(name string) func(context.Context) error {
	return func(ctx context.Context) error {
		fmt.Printf("→ %s start\n", name)
		time.Sleep(100 * time.Millisecond)
		fmt.Printf("✓ %s done\n", name)
		return nil
	}
}

func main() {
	d := dag.New(dag.WithMaxParallel(4)).Add(
		dag.Node{Name: "build", Run: step("build")},
		dag.Node{Name: "test", DependsOn: []string{"build"}, Run: step("test")},
		dag.Node{Name: "lint", DependsOn: []string{"build"}, Run: step("lint")},
		dag.Node{Name: "deploy", DependsOn: []string{"test", "lint"}, Run: step("deploy")},
	)

	if err := d.Validate(); err != nil {
		panic(err)
	}
	if err := d.Run(context.Background()); err != nil {
		fmt.Println("dag failed:", err)
		return
	}
	fmt.Println("pipeline finished")
}
