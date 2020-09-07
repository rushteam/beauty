package parser

import (
	"fmt"
)

//NewLexer ..
func NewLexer(s *Scanner) *Lexer {
	// scanner.TokenString(12)
	return &Lexer{s: s}
}

//Lexer ..
type Lexer struct {
	s *Scanner
}

//Lex ..
func (l *Lexer) Lex(lval *BeautySymType) int {
	tok := l.s.Scan()
	// fmt.Println("tok", string(tok))
	tok = at_opt
	lval.val = "@auth jwt"
	// var buf bytes.Buffer
	// for {
	// l.s.Scan()
	// c, err := l.s.read()
	// if err != nil {
	// 	return EOF
	// }
	// switch c {
	// case ' ', '\t':
	// 	continue
	// case '\n', '\v', '\r':
	// 	l.line++
	// 	continue
	// }
	// buf.WriteRune(c)
	// // lval.val = v.Val
	// switch strings.ToLower(buf.String()) {
	// case "@":
	// 	return l.scanAt()
	// default:
	// 	return ILLEGAL
	// }
	// }
	return int(tok)
}

func (l *Lexer) Error(msg string) {
	fmt.Printf("syntax error: %s (line: %d)\n", msg, l.s.line)
}
