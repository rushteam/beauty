package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

func TestParser(t *testing.T) {
	content := `
service helloworld {}
service demo {
	rpc Index(getRequest) returns (getResponse)
}
`
	stmts, err := Parser(strings.NewReader(content), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) == 0 {
		t.Fatal("expected at least 1 statement, got 0")
	}

	var services []string
	var rpcs []string
	ast.Inspect(stmts, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Service:
			services = append(services, n.Name)
		case *ast.RPC:
			rpcs = append(rpcs, fmt.Sprintf("%s(%s) returns %s", n.Handler, n.Request, n.Response))
		}
		return true
	})

	if len(services) != 2 || services[0] != "helloworld" || services[1] != "demo" {
		t.Fatalf("services = %v, want [helloworld demo]", services)
	}
	if len(rpcs) != 1 || rpcs[0] != "Index(getRequest) returns getResponse" {
		t.Fatalf("rpcs = %v", rpcs)
	}
}
