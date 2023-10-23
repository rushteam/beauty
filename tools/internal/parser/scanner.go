package parser

import (
	"fmt"
	"io"
	"text/scanner"

	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

// Parser ..
func Parser(src io.Reader, filename string) ([]*ast.Stmt, error) {
	s := newScanner(src, filename)
	if yyParse(s) != 0 {
		return nil, s.Err
	}
	return s.Stmts, nil
}

// newScanner ..
func newScanner(src io.Reader, filename string) *Scanner {
	p := scanner.Position{Filename: filename}
	s := &Scanner{
		s: &scanner.Scanner{Position: p},
	}
	s.s.Init(src)
	s.s.Error = func(ss *scanner.Scanner, msg string) {
		s.Error(msg)
	}
	return s
}

// Scanner ..
type Scanner struct {
	s     *scanner.Scanner
	Stmts []*ast.Stmt
	Err   error
}

// Lex ..
func (s *Scanner) Error(msg string) {
	s.Err = fmt.Errorf("scanner: %v %v\n", msg, s.s.Pos())
}

var identTokens = map[string]int{
	"service": Service,
	"rpc":     Rpc,
	"returns": Returns,
	"route":   Route,
}

// Lex ..
func (s *Scanner) Lex(lval *yySymType) int {
	var tok int
	var val, v string
	var stateFn state = s.rootState
	for {
		if stateFn == nil {
			break
		}
		tok, v, stateFn = stateFn()
		val = val + v
	}
	// fmt.Println("token:", tok, val)
	lval.token = val
	// Ident
	// Int
	// Float
	// Char
	// String
	// RawString
	// Comment
	//ILLEGAL
	return int(tok)
}

type state func() (int, string, state)

func (s *Scanner) rootState() (int, string, state) {
	tok, val := s.s.Scan(), s.s.TokenText()
	switch tok {
	case scanner.EOF:
		return EOF, val, nil
	case scanner.Ident:
		if t, ok := identTokens[val]; ok {
			return t, val, nil
		}
		return Ident, val, nil
	case scanner.String:
		return String, val, nil
	case '@':
		return String, val, s.atState
	}
	return int(tok), val, nil
}
func (s *Scanner) atState() (int, string, state) {
	var atTokens = map[string]int{
		"route": Route,
	}
	tok, val := s.s.Scan(), s.s.TokenText()
	switch tok {
	case scanner.EOF:
		return EOF, val, nil
	default:
		if t, ok := atTokens[val]; ok {
			return t, val, nil
		}
		return ILLEGAL, val, nil
	}
}
