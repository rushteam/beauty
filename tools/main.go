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
	content := `
@auth jwt
//test
@auth jwt

`
	// s := parser.NewScanner(strings.NewReader(content))
	s := &parser.Scanner{}
	s.Init(strings.NewReader(content))
	parser.BeautyParse(parser.NewLexer(s))
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
