package parser

import (
	"fmt"
	"io"
)

// type baseState struct {
// 	r          *bufio.Reader
// 	lineNumber *int
// }

// //RootState ..
// type RootState struct{}

// //NewParser ..
// func NewParser(src string) *SvcAst {
// 	if err != nil {
// 		return nil, err
// 	}
// 	sa := &SvcAst{}
// 	return sa
// }

// Parser represents a parser.
type Parser struct {
	s     *Scanner
	state struct {
		tok Token  // last read token
		lit string // last read literal
		n   int    // buffer size (max=1)
	}
}

// NewParser returns a new instance of Parser.
func NewParser(r io.Reader) *Parser {
	return &Parser{s: NewScanner(r)}
}

//Parse ..
func (p *Parser) Parse() {
	tok, lit := p.s.Scan()
	state := startState()
	fmt.Println(tok, lit)
	return
}
func startState() {

}
