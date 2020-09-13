package main

import (
	"strings"

	"github.com/rushteam/beauty/tools/parser"
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
	parser.BeautyParse(parser.NewScanner(strings.NewReader(content)))

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
