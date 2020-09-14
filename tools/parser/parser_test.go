package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rushteam/beauty/tools/parser/ast"
)

func TestParser(t *testing.T) {
	content := `
service helloworld (
	@route GET|POST "/index/:id"
    rpc Index(getRequest) returns (getResponse)
    rpc Helloworld(getRequest) returns (getResponse)
)
`
	stmts, err := Parser(strings.NewReader(content), "")
	if err != nil {
		t.Error(err)
	}
	// fmt.Printf("%+v", stmts[0])
	var gen = ""
	ast.Inspect(stmts, func(node ast.Node) bool {
		// fmt.Printf("node: %+v \n", n)
		switch n := node.(type) {
		case *ast.Service:
			gen += fmt.Sprintf("service %v (\n", n.Name)
		case *ast.Route:
			gen += fmt.Sprintf("@route %v %v\n", strings.Join(n.Methods, "|"), n.URI)
		case *ast.RPC:
			gen += fmt.Sprintf("rpc %v(%v) returns %v\n", n.Handler, n.Request, n.Response)
		}
		return true
	})
	t.Log(gen)
	t.Fail()
	// v, _ := json.Marshal(stmts)
	// fmt.Println(string(v))
	// ast.Inspect(stmts, func(node ast.Node) bool {
	// 	fmt.Printf("node: %+v \n", node)
	// 	// switch n := node.(type) {
	// 	// case *ast.Service:
	// 	// 	fmt.Println(n.Name)
	// 	// }
	// 	return true
	// })
}
