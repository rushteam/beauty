package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rushteam/beauty/tools/parser"
	"github.com/rushteam/beauty/tools/parser/ast"
)

func main() {
	// 	content := `
	// @doc(summary: desc in one line)
	// @auth jwt
	// service (
	//     @rest /index/:name
	//     rpc Index(getRequest) returns (getResponse)
	// )
	// `
	// @auth jwt
	// @auth jwt
	// case Ident:
	// 	t :=
	// case '@':
	// 	switch strings.ToLower(s.TokenText()) {
	// 	case "@route":
	// 		tok = Route
	// 	}
	// }
	content := `
service helloworld (
	@route GET|POST "/index/:id"
    rpc Index(getRequest) returns (getResponse)
    rpc Helloworld(getRequest) returns (getResponse)
)
`
	stmts, err := parser.Parser(strings.NewReader(content), "")
	if err != nil {
		fmt.Println(err)
	}
	v, _ := json.Marshal(stmts)
	fmt.Println(string(v))
	// fmt.Printf("%+v", stmts[0])
	ast.Inspect(stmts, func(node ast.Node) bool {
		fmt.Printf("node: %+v \n", node)
		// switch n := node.(type) {
		// case *ast.Service:
		// 	fmt.Println(n.Name)
		// }
		return true
	})

	// fi := bufio.NewReader(os.NewFile(0, "stdin"))
	// for {
	// 	var eqn string
	// 	var ok bool

	// 	fmt.Printf("equation: ")
	// 	if eqn, ok = readline(fi); ok {
	// 		&Scanner{s: eqn}
	// 	} else {
	// 		break
	// 	}
	// }
}
