package parser

import (
	"fmt"
	"io"
	"strings"
)

//NewScanner ..
func NewScanner(src io.Reader) BeautyLexer {
	s := &Scanner{}
	s.Init(src)
	s.ErrorFunc = func(s *Scanner, msg string) {
		fmt.Printf("scanner: %s %s\n", msg, s.Pos())
	}
	return s
}

//Lex ..
func (s *Scanner) Error(msg string) {
	if s.ErrorFunc != nil {
		s.ErrorFunc(s, msg)
	}
}

//Lex ..
func (s *Scanner) Lex(lval *BeautySymType) int {
	tok := s.Scan()
	val := s.TokenText()
	switch tok {
	case EOF:
		return EOF
	case ILLEGAL:
		return ILLEGAL
	case Ident:
		t := strings.ToLower(val)
		switch t {
		case "service":
			tok = Service
		case "rpc":
			tok = Rpc
		case "returns":
			tok = Returns
			// case "(":
			// 	tok = '('
			// case ")":
			// 	tok = ')'
			// case "@":
			// 	tok = ')'
		}
	case '@':
		switch strings.ToLower(s.TokenText()) {
		case "@route":
			tok = Route
		}
	}
	fmt.Println("token:", tok, val)
	lval.token = val
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
